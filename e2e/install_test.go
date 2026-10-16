//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/firefox"
	"github.com/chriscz/mirrormark/internal/host"
	"github.com/chriscz/mirrormark/internal/ws"
)

// roundDone waits until the host has finished a round for b with nothing
// wrong: its status message clears status.error in storage.
func roundDone(t *testing.T, b *Browser, what string) {
	t.Helper()
	eventually(t, 10*time.Second, what, func() bool {
		return eval[bool](t, b, `chrome.storage.local.get("status").then((r) => r.status !== undefined && r.status.error === null)`)
	})
}

func TestInstallAndBind(t *testing.T) {
	e := setup(t)
	if e.DBA == e.DBB {
		t.Fatalf("both profiles bound to %s", e.DBA)
	}
	for _, c := range []struct {
		b  *Browser
		id string
	}{{e.A, e.DBA}, {e.B, e.DBB}} {
		// Chrome installed the CRX from the external extensions file under
		// the id install printed.
		self := eval[struct {
			ID          string `json:"id"`
			InstallType string `json:"installType"`
			Enabled     bool   `json:"enabled"`
		}](t, c.b, `chrome.management.getSelf()`)
		if self.ID != e.ExtID || self.InstallType != "sideload" || !self.Enabled {
			t.Errorf("%s: extension %+v, want id %s, sideload, enabled", c.b.Name, self, e.ExtID)
		}
		if got := eval[string](t, c.b, `chrome.storage.local.get("db").then((r) => r.db)`); got != c.id {
			t.Errorf("%s is bound to %q, want %q", c.b.Name, got, c.id)
		}
		d, err := db.LoadDB(e.Paths.ProfileFile(c.id))
		if err != nil || d.Profile.Path != filepath.Join(c.b.UDD, "Default") {
			t.Errorf("%s database %+v, %v", c.id, d, err)
		}
		if got := eval[string](t, c.b, `chrome.storage.local.get("token").then((r) => r.token)`); got != d.Profile.Token || got == "" {
			t.Errorf("%s holds token %q, the database %q", c.b.Name, got, d.Profile.Token)
		}
		roundDone(t, c.b, c.b.Name+" round after binding")
	}

	// Without --profile, install lists and changes nothing.
	out := e.mm(t, "install", "--user-data-dir", e.A.UDD)
	if !strings.Contains(out, "  0  Default") || !strings.Contains(out, "bound") || !strings.Contains(out, "Nothing was changed.") {
		t.Fatalf("listing:\n%s", out)
	}

	// Installing again in a bound profile keeps its database, and the
	// extension lets go of the old host and connects to a new one.
	old := connections(t, e, e.DBA)
	if len(old) != 1 {
		t.Fatalf("want one connection for a before binding again, got %v", old)
	}
	if out := e.mm(t, "install", "--user-data-dir", e.A.UDD, "--profile", "Default"); !strings.Contains(out, "bound: Default -> "+e.DBA) {
		t.Fatalf("second install rebound to another database:\n%s", out)
	}
	// The host logs its hello only once it holds the database's lock, so a
	// second hello means the old host let go of it.
	eventually(t, 10*time.Second, "a new connection for a", func() bool { return len(connections(t, e, e.DBA)) == 2 })
	eventually(t, 10*time.Second, "the old connection to close", func() bool { return closed(t, e, old[0]) })
	// With the old host gone, a status after an edit comes from the new one.
	eval[bool](t, e.A, `chrome.storage.local.remove("status").then(() => true)`)
	e.A.Add(t, "other", "Example", "https://example.com/")
	roundDone(t, e.A, "a round in the new host")
	log := hostLog(t, e)
	if n := strings.Count(log, "msg=connected pid="); n < 3 {
		t.Errorf("want at least 3 hellos in the host log (a, b, a again), got %d", n)
	}
	if strings.Contains(log, "level=WARN") || strings.Contains(log, "level=ERROR") {
		t.Errorf("the host warned while binding")
	}

	// A restart keeps the binding: the service worker starts with the
	// profile and connects again without any page open.
	before := time.Now()
	e.A.Close()
	e.A = e.launch("a")
	eventually(t, 15*time.Second, "a hello after restart", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
}

// impostor is a program on the first port that accepts any upgrade and
// answers every hello with answer, as another user's program could, or
// never answers while answer is nil.
type impostor struct {
	srv   *http.Server
	mu    sync.Mutex
	types []string // of the messages it received
	// For each database named in a hello: the hellos, the connections
	// open now, and the most open at once.
	hellos, live, peak map[string]int
}

// newImpostor starts an impostor on the first port; it stops when the test
// ends, or on Close.
func newImpostor(t *testing.T, answer *atomic.Pointer[host.Msg]) *impostor {
	ports, err := extension.Ports()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0])))
	if err != nil {
		t.Fatal(err)
	}
	im := &impostor{hellos: map[string]int{}, live: map[string]int{}, peak: map[string]int{}}
	im.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 1<<20)
		if err != nil {
			return
		}
		var named string // the database in this connection's hello
		defer func() {
			im.mu.Lock()
			if named != "" {
				im.live[named]--
			}
			im.mu.Unlock()
		}()
		defer c.Close(1000, "")
		for {
			b, err := c.Read()
			if err != nil {
				return
			}
			var m host.Msg
			json.Unmarshal(b, &m)
			im.mu.Lock()
			im.types = append(im.types, m.Type)
			if m.Type == "hello" && named == "" {
				named = m.DB
				im.hellos[named]++
				im.live[named]++
				im.peak[named] = max(im.peak[named], im.live[named])
			}
			im.mu.Unlock()
			if a := answer.Load(); m.Type == "hello" && a != nil {
				b, _ := json.Marshal(a)
				c.Write(b)
			}
		}
	})}
	go im.srv.Serve(ln)
	t.Cleanup(im.Close)
	return im
}

// Types are the types of the messages the impostor received.
func (im *impostor) Types() []string {
	im.mu.Lock()
	defer im.mu.Unlock()
	return slices.Clone(im.types)
}

// Conns are, for database db, how many hellos the impostor received, how
// many of their connections are open now, and how many were at most.
func (im *impostor) Conns(db string) (hellos, live, peak int) {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.hellos[db], im.live[db], im.peak[db]
}

// Close stops the impostor and frees the port; the connections it has
// taken over go when their handlers return.
func (im *impostor) Close() { im.srv.Close() }

// popupError is the error b's popup shows, or "" if none.
func popupError(t *testing.T, b *Browser) string {
	t.Helper()
	return eval[string](t, b, `chrome.storage.local.get("status").then((r) => r.status?.error ?? "")`)
}

// popupView is what the popup page b last opened shows: whether its
// spinner and the text beside it are showing, and its error lines.
type popupView struct {
	Busy  bool   `json:"busy"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

func popupShows(t *testing.T, b *Browser) popupView {
	t.Helper()
	return eval[popupView](t, b, `({
  busy: !document.getElementById("busy").hidden,
  text: document.getElementById("busy").textContent,
  error: document.getElementById("error").textContent,
})`)
}

// reconnectIn is how long until b's reconnect alarm fires, or a negative
// duration if none is set.
func reconnectIn(t *testing.T, b *Browser) time.Duration {
	t.Helper()
	ms := eval[float64](t, b, `chrome.alarms.get("reconnect").then((a) => (a ? a.scheduledTime - Date.now() : -1))`)
	return time.Duration(ms) * time.Millisecond
}

// TestReconnectsAfterServerRestart: with the server gone, the popup says
// mirrormark is not running. A program on the first port that answers with
// an error of its own gets a fixed text in the popup, not its words. Once
// the server is back, on the second port because that program holds the
// first, the extension skips the program, tells it nothing more than its
// hello, and syncs again. A known error from the server outranks the
// program's wrong proof, and a profile with no token is told to bind again.
func TestReconnectsAfterServerRestart(t *testing.T) {
	for _, k := range []string{"chrome", "firefox"} {
		t.Run(k, func(t *testing.T) { testReconnectsAfterServerRestart(t, k) })
	}
}

func testReconnectsAfterServerRestart(t *testing.T, kind string) {
	e := setupPair(t, kind, kind)
	roundDone(t, e.A, "a's first round")

	// A restart, as `systemctl --user restart` does: the extension tries
	// again within seconds, not at its 30 s alarm, and the popup clears.
	before := time.Now()
	e.stopServer(t)
	e.serve(t)
	eventually(t, 8*time.Second, "a's connection soon after a restart", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
	t.Logf("a reconnected %v after the restart began", time.Since(before).Round(time.Millisecond))
	roundDone(t, e.A, "a round after a restart")
	eventually(t, 5*time.Second, "the open popup to clear", func() bool {
		v := popupShows(t, e.A)
		return !v.Busy && v.Error == ""
	})

	e.stopServer(t)
	// The close says so in plain words, never with a close code, and the
	// attempts after it find no server.
	eventually(t, 5*time.Second, "the popup to say mirrormark stopped", func() bool {
		return popupError(t, e.A) != ""
	})
	if msg := popupError(t, e.A); msg != "mirrormark restarted or stopped; reconnecting…" && !strings.HasPrefix(msg, "mirrormark is not running") {
		t.Fatalf("after the server stopped, the popup shows %q", msg)
	}
	eventually(t, 70*time.Second, "the popup to say mirrormark is not running", func() bool {
		return strings.HasPrefix(popupError(t, e.A), "mirrormark is not running")
	})
	var answer atomic.Pointer[host.Msg]
	answer.Store(&host.Msg{V: host.Version, Type: "error", Code: "evil", Message: "PEER TEXT"})
	im := newImpostor(t, &answer)
	ports, err := extension.Ports()
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 70*time.Second, "the popup to name the unexpected answer", func() bool {
		return strings.HasPrefix(popupError(t, e.A), fmt.Sprintf("unexpected answer on port %d", ports[0]))
	})
	if msg := popupError(t, e.A); strings.Contains(msg, "PEER") || strings.Contains(msg, "evil") {
		t.Fatalf("the popup shows the program's own words: %q", msg)
	}
	answer.Store(&host.Msg{V: host.Version, Type: "challenge", Nonce: strings.Repeat("0", 64), Proof: strings.Repeat("0", 64)})
	before = time.Now()
	e.serve(t) // the first port is taken, so it listens on the second
	eventually(t, 70*time.Second, "a proven connection after the server came back", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
	roundDone(t, e.A, "a round after the restart")

	// A binding to a database the server lacks: the server's db-missing
	// shows, not the first port's failed proof. A new binding reconnects
	// after a second, whatever alarm is set.
	eval[bool](t, e.A, `chrome.storage.local.set({ db: "gone-db" }).then(() => true)`)
	eventually(t, 15*time.Second, "the popup to show db-missing", func() bool {
		return strings.HasPrefix(popupError(t, e.A), fmt.Sprintf("db-missing: mirrormark has no database for this profile; run `mirrormark install` for this profile again (port %d)", ports[1]))
	})
	eval[bool](t, e.A, fmt.Sprintf(`chrome.storage.local.set({ db: %q }).then(() => true)`, e.DBA))
	roundDone(t, e.A, "a round after binding back")

	// A binding without a token, as from a build before the server.
	token := eval[string](t, e.A, `chrome.storage.local.get("token").then((r) => r.token)`)
	eval[bool](t, e.A, `chrome.storage.local.remove("token").then(() => true)`)
	eventually(t, 15*time.Second, "the popup to say the profile is not bound yet", func() bool {
		return strings.HasPrefix(popupError(t, e.A), "not bound yet: run `mirrormark install --profile")
	})
	eval[bool](t, e.A, fmt.Sprintf(`chrome.storage.local.set({ token: %q }).then(() => true)`, token))
	roundDone(t, e.A, "a round with the token back")

	if types := im.Types(); !slices.Contains(types, "hello") || slices.Contains(types, "proof") || slices.Contains(types, "tree") {
		t.Fatalf("the program on the first port received %q; want hellos only", types)
	}
}

// TestPopupConnects: opening the popup tries to connect at once, rather
// than at the extension's next retry, and shows a spinner while it tries.
// Opening it again during an attempt starts no second one.
func TestPopupConnects(t *testing.T) {
	for _, k := range []string{"chrome", "firefox"} {
		t.Run(k, func(t *testing.T) { testPopupConnects(t, k) })
	}
}

func testPopupConnects(t *testing.T, kind string) {
	e := setupPair(t, kind, kind)
	roundDone(t, e.A, "a's first round")
	e.stopServer(t)
	eventually(t, 70*time.Second, "the popup to say mirrormark is not running", func() bool {
		return strings.HasPrefix(popupError(t, e.A), "mirrormark is not running")
	})

	// A program on the first port that never answers holds each attempt
	// for the extension's 5 s limit, long enough to see the spinner.
	var answer atomic.Pointer[host.Msg]
	im := newImpostor(t, &answer)
	e.open(t, e.A)
	eventually(t, 3*time.Second, "the popup's spinner", func() bool {
		v := popupShows(t, e.A)
		return v.Busy && v.Text == "Connecting to mirrormark…" && v.Error == ""
	})
	eventually(t, 3*time.Second, "the popup's attempt to reach the program", func() bool {
		_, live, _ := im.Conns(e.DBA)
		return live == 1
	})
	for range 3 {
		e.open(t, e.A)
	}
	if _, live, _ := im.Conns(e.DBA); live != 1 || !popupShows(t, e.A).Busy {
		t.Fatalf("the first attempt ended before the popup had opened again; %d connections open", live)
	}
	// Count the attempts as soon as this one ends: a dial that timed out
	// may set a quick retry, unless an earlier close in this worker's life
	// used it up, and that retry is not the popup's.
	var hellos, peak int
	eventually(t, 10*time.Second, "the popup to say mirrormark is not running", func() bool {
		hellos, _, peak = im.Conns(e.DBA)
		v := popupShows(t, e.A)
		return !v.Busy && strings.HasPrefix(v.Error, "mirrormark is not running")
	})
	if hellos != 1 || peak != 1 {
		t.Fatalf("four opens of the popup made %d attempts, %d at once; want 1", hellos, peak)
	}
	im.Close()
	// Let any quick retry run, against no server, so it neither connects
	// for the popup below nor clears the alarm read here.
	time.Sleep(3 * time.Second)

	// With the server back, opening the popup connects well before the
	// alarm would.
	wait := reconnectIn(t, e.A)
	if wait < 10*time.Second {
		t.Fatalf("the reconnect alarm is %v away; want it far enough off to tell it from the popup", wait)
	}
	e.serve(t)
	before := time.Now()
	e.open(t, e.A)
	eventually(t, 5*time.Second, "a's connection after the popup opened", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
	t.Logf("a connected %v after the popup opened, %v before its alarm", time.Since(before).Round(time.Millisecond), wait.Round(time.Second))
	eventually(t, 5*time.Second, "the popup to clear", func() bool {
		v := popupShows(t, e.A)
		return !v.Busy && v.Error == ""
	})
}

// TestBindStartsChrome follows a user who runs install's second pass
// before Chrome has installed the extension in the profile: install says
// so and opens nothing. Once Chrome has run there and closed, install
// starts Chrome itself, opens the page once and binds.
func TestBindStartsChrome(t *testing.T) {
	e := newEnv(t)
	e.firstInstall(t)
	e.noSandbox(t)
	out, code := e.mmExit(t, e.installArgs("a")...)
	if code != 1 || !strings.Contains(out, "not ready: Default: Chrome has not installed mirrormark in this profile yet.") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(e.udd("a"), "SingletonLock")); err == nil {
		t.Fatal("install started Chrome in a profile that was not ready")
	}
	// The user starts Chrome, which installs the extension, and quits it,
	// which writes Preferences at once.
	a := e.launch("a")
	e.open(t, a)
	a.Close()
	t.Cleanup(func() { stopChrome(t, e.udd("a")) })
	start := time.Now()
	id := e.bind("a")
	t.Logf("bound in %v with one open", time.Since(start).Round(time.Millisecond))
	stopChrome(t, e.udd("a"))
	e.A = e.launch("a")
	e.open(t, e.A)
	if got := eval[string](t, e.A, `chrome.storage.local.get("db").then((r) => r.db)`); got != id {
		t.Errorf("a is bound to %q, want %q", got, id)
	}
	roundDone(t, e.A, "a round after Chrome restarted")
}

func TestFirefoxInstallAndBind(t *testing.T) {
	e := setupPair(t, "firefox", "firefox")
	if e.DBA != "firefox-a" || e.DBB != "firefox-b" {
		t.Fatalf("bound to %s and %s", e.DBA, e.DBB)
	}
	for _, c := range []struct {
		b  *Browser
		id string
	}{{e.A, e.DBA}, {e.B, e.DBB}} {
		if got := eval[string](t, c.b, `chrome.runtime.id`); got != extension.GeckoID {
			t.Errorf("%s: extension id %q", c.b.Name, got)
		}
		if got := eval[string](t, c.b, `chrome.storage.local.get("db").then((r) => r.db)`); got != c.id {
			t.Errorf("%s is bound to %q, want %q", c.b.Name, got, c.id)
		}
		d, err := db.LoadDB(e.Paths.ProfileFile(c.id))
		if err != nil || d.Profile.Path != c.b.UDD || d.Profile.Browser != "firefox" {
			t.Errorf("%s database %+v, %v", c.id, d, err)
		}
		roundDone(t, c.b, c.b.Name+" round after binding")
	}
	if !strings.Contains(hostLog(t, e), "browser=firefox/159.0") {
		t.Error("no hello from firefox/159.0 in the host log")
	}

	// Binding a running profile again goes through D-Bus to the running
	// Firefox, and the extension moves to a new host.
	old := connections(t, e, e.DBA)
	if out := e.mm(t, "install", "--browser", "firefox", "--profile", "a"); !strings.Contains(out, "bound: a -> "+e.DBA) {
		t.Fatalf("second install:\n%s", out)
	}
	eventually(t, 10*time.Second, "a new connection for a", func() bool { return len(connections(t, e, e.DBA)) == len(old)+1 })
	eventually(t, 10*time.Second, "the old connection to close", func() bool { return closed(t, e, old[len(old)-1]) })

	// A restart keeps the binding: the event page starts with the profile.
	before := time.Now()
	e.A.Close()
	e.A = e.launch("a")
	eventually(t, 15*time.Second, "a hello after restart", func() bool { return db.Connected(e.Paths, e.DBA, before) })
	if log := hostLog(t, e); strings.Contains(log, "level=WARN") || strings.Contains(log, "level=ERROR") {
		t.Errorf("the host warned:\n%s", log)
	}
}

// TestFirefoxBindStartsFirefox follows a user who runs install's second
// pass before Firefox has started in the profile since the first: install
// says so and opens nothing. Once Firefox has run there and closed,
// install starts Firefox itself, opens the page once and binds.
func TestFirefoxBindStartsFirefox(t *testing.T) {
	e := newEnv(t)
	e.kinds = map[string]string{"a": "firefox", "b": "firefox"}
	e.firstInstall(t)
	out, code := e.mmExit(t, e.installArgs("a")...)
	if code != 1 || !strings.Contains(out, "not ready: a: Firefox Developer Edition has not installed mirrormark in this profile yet.") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if firefoxPID(t, e.udd("a")) != 0 {
		t.Fatal("install started Firefox in a profile that was not ready")
	}
	// The user starts Firefox, which installs the add-on and records its
	// UUID, and quits it.
	a := e.launch("a")
	e.open(t, a)
	a.Close()
	t.Cleanup(func() { stopFirefox(t, e.udd("a")) })
	start := time.Now()
	id := e.bind("a")
	t.Logf("bound in %v with one open", time.Since(start).Round(time.Millisecond))
	stopFirefox(t, e.udd("a"))
	e.A = e.launch("a")
	e.open(t, e.A)
	if got := eval[string](t, e.A, `chrome.storage.local.get("db").then((r) => r.db)`); got != id {
		t.Errorf("a is bound to %q, want %q", got, id)
	}
}

// firefoxPID is the pid of the Firefox running in the profile, or 0. The
// profile's lock is a symlink to "<ip>:+<pid>", which Firefox leaves
// behind when it exits.
func firefoxPID(t *testing.T, dir string) int {
	t.Helper()
	lock := filepath.Join(dir, "lock")
	target, err := os.Readlink(lock)
	if err != nil {
		return 0 // never started
	}
	pid, err := strconv.Atoi(target[strings.LastIndexByte(target, '+')+1:])
	if err != nil {
		t.Fatalf("%s: %v", lock, err)
	}
	if exited(pid) {
		return 0
	}
	return pid
}

// stopFirefox quits a Firefox the test did not start, as stopChrome does.
func stopFirefox(t *testing.T, dir string) {
	t.Helper()
	pid := firefoxPID(t, dir)
	if pid == 0 {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("stop Firefox %d: %v", pid, err)
	}
	eventually(t, 20*time.Second, "Firefox to exit", func() bool { return exited(pid) })
}

// noSandbox puts a google-chrome first on PATH for the Chrome that
// install starts. It adds the flags the tests' own launches pass:
// --no-sandbox because Docker lacks the namespaces Chrome's sandbox needs,
// and --no-first-run because those launches skip the first run, so a later
// start without the flag would show the first-run pages a real user's
// Chrome is long past.
func (e *Env) noSandbox(t *testing.T) {
	t.Helper()
	dir := filepath.Join(e.Home, "bin")
	script := "#!/bin/sh\nexec /usr/bin/google-chrome --no-sandbox --no-first-run \"$@\"\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "google-chrome"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, kv := range e.env {
		if strings.HasPrefix(kv, "PATH=") {
			e.env[i] = "PATH=" + dir + ":" + kv[len("PATH="):]
		}
	}
}

// stopChrome quits a Chrome the test did not start, as a user's quit
// would: SIGTERM makes Chrome save its files and exit. Its SingletonLock
// names its pid. Chrome leaves the lock behind on SIGTERM, and the next
// start takes it over, so stopChrome waits for the process instead.
func stopChrome(t *testing.T, udd string) {
	t.Helper()
	lock := filepath.Join(udd, "SingletonLock")
	target, err := os.Readlink(lock)
	if err != nil {
		return // never started
	}
	pid, err := strconv.Atoi(target[strings.LastIndexByte(target, '-')+1:])
	if err != nil {
		t.Fatalf("%s: %v", lock, err)
	}
	if exited(pid) {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("stop Chrome %d: %v", pid, err)
	}
	eventually(t, 20*time.Second, "Chrome to exit", func() bool { return exited(pid) })
}

// exited reports whether process pid has exited. One that is not this
// test's child lingers as a zombie until something reaps it, which has
// exited all the same.
func exited(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	// The state follows the parenthesised command name.
	return err != nil || strings.HasPrefix(string(stat[bytes.LastIndexByte(stat, ')')+1:]), " Z")
}

var (
	connectedRe    = regexp.MustCompile(`msg=connected pid=\d+ conn=(\d+) db=(\S+)`)
	disconnectedRe = regexp.MustCompile(`msg=disconnected pid=\d+ conn=(\d+)`)
)

// connections lists the numbers of the connections whose hello for
// database id the server accepted, in the order of the log.
func connections(t *testing.T, e *Env, id string) []string {
	t.Helper()
	var out []string
	for _, m := range connectedRe.FindAllStringSubmatch(hostLog(t, e), -1) {
		if m[2] == id {
			out = append(out, m[1])
		}
	}
	return out
}

// closed reports whether connection conn has ended.
func closed(t *testing.T, e *Env, conn string) bool {
	for _, m := range disconnectedRe.FindAllStringSubmatch(hostLog(t, e), -1) {
		if m[1] == conn {
			return true
		}
	}
	return false
}

func hostLog(t *testing.T, e *Env) string {
	t.Helper()
	b, err := os.ReadFile(e.Paths.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestBraveInstallAndBind: Brave installs the extension under Chrome's id
// and binds as Chrome does, and uninstalling Brave leaves Chrome syncing.
func TestBraveInstallAndBind(t *testing.T) {
	e := setupPair(t, "chrome", "brave")
	if !strings.HasPrefix(e.DBB, "brave-") {
		t.Fatalf("Brave bound to %s", e.DBB)
	}
	self := eval[struct {
		ID          string `json:"id"`
		InstallType string `json:"installType"`
		Enabled     bool   `json:"enabled"`
	}](t, e.B, `chrome.management.getSelf()`)
	if self.ID != e.ExtID || self.InstallType != "sideload" || !self.Enabled {
		t.Errorf("Brave: extension %+v, want id %s, sideload, enabled", self, e.ExtID)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBB))
	if err != nil || d.Profile.Browser != "brave" || d.Profile.Path != filepath.Join(e.B.UDD, "Default") ||
		d.Profile.Origin != "chrome-extension://"+e.ExtID {
		t.Errorf("%s database %+v, %v", e.DBB, d, err)
	}
	roundDone(t, e.A, "Chrome's round after binding")
	roundDone(t, e.B, "Brave's round after binding")

	e.B.Close()
	out := e.mm(t, "uninstall", "--browser", "brave")
	if _, err := os.Stat(chrome.Brave.ExternalFile(e.ExtID)); !os.IsNotExist(err) {
		t.Errorf("Brave's external file is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.Paths.Data, "mirrormark.crx")); err != nil {
		t.Fatalf("the CRX went with Brave: %v", err)
	}
	if !strings.Contains(out, "Kept the mirrormark service, which Chrome still uses.") {
		t.Errorf("uninstall did not keep the service for Chrome:\n%s", out)
	}
	eval[bool](t, e.A, `chrome.storage.local.remove("status").then(() => true)`)
	e.A.Add(t, "other", "Example", "https://example.com/")
	roundDone(t, e.A, "Chrome's round after Brave's uninstall")
}

// TestLibreWolfInstallAndBind: LibreWolf takes Developer Edition's route,
// and uninstalling it resets its prefs and leaves Developer Edition
// syncing.
func TestLibreWolfInstallAndBind(t *testing.T) {
	e := setupPair(t, "librewolf", "firefox")
	if e.DBA != "librewolf-a" || e.DBB != "firefox-b" {
		t.Fatalf("bound to %s and %s", e.DBA, e.DBB)
	}
	if got := eval[string](t, e.A, `chrome.runtime.id`); got != extension.GeckoID {
		t.Errorf("LibreWolf: extension id %q", got)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBA))
	if err != nil || d.Profile.Browser != "librewolf" || d.Profile.Path != e.A.UDD || !strings.HasPrefix(d.Profile.Origin, "moz-extension://") {
		t.Errorf("%s database %+v, %v", e.DBA, d, err)
	}
	roundDone(t, e.A, "LibreWolf's round after binding")
	roundDone(t, e.B, "Developer Edition's round after binding")

	e.A.Close()
	out := e.mm(t, "uninstall", "--browser", "librewolf")
	if _, err := os.Stat(firefox.XPIPath(e.udd("a"))); !os.IsNotExist(err) {
		t.Errorf("the XPI is still in LibreWolf's profile: %v", err)
	}
	if user, _ := os.ReadFile(filepath.Join(e.udd("a"), "user.js")); !bytes.Contains(user, []byte(`user_pref("xpinstall.signatures.required", true);`)) {
		t.Errorf("LibreWolf's user.js not put back:\n%s", user)
	}
	if !strings.Contains(out, "Kept the mirrormark service, which Firefox Developer Edition still uses.") {
		t.Errorf("uninstall did not keep the service for Developer Edition:\n%s", out)
	}
	eval[bool](t, e.B, `chrome.storage.local.remove("status").then(() => true)`)
	e.B.Add(t, "other", "Example", "https://example.com/")
	roundDone(t, e.B, "Developer Edition's round after LibreWolf's uninstall")
}
