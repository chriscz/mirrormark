//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/db"
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
		roundDone(t, c.b, c.b.Name+" round after binding")
	}

	// Installing again in a bound profile keeps its database, and the
	// extension lets go of the old host and connects to a new one.
	old := hostPIDs(t, e, e.DBA)
	if len(old) != 1 {
		t.Fatalf("want one host for a before binding again, got pids %v", old)
	}
	if out := e.mm(t, "install", "--user-data-dir", e.A.UDD); !strings.Contains(out, "bound: Default -> "+e.DBA) {
		t.Fatalf("second install rebound to another database:\n%s", out)
	}
	// The host logs its hello only once it holds the database's lock, so a
	// second hello means the old host let go of it.
	eventually(t, 10*time.Second, "a new host for a", func() bool { return len(hostPIDs(t, e, e.DBA)) == 2 })
	eventually(t, 10*time.Second, "the old host to exit", func() bool { return exited(old[0]) })
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
	old := hostPIDs(t, e, e.DBA)
	if out := e.mm(t, "install", "--browser", "firefox", "--profile", "a"); !strings.Contains(out, "bound: a -> "+e.DBA) {
		t.Fatalf("second install:\n%s", out)
	}
	eventually(t, 10*time.Second, "a new host for a", func() bool { return len(hostPIDs(t, e, e.DBA)) == len(old)+1 })
	eventually(t, 10*time.Second, "the old host to exit", func() bool { return exited(old[len(old)-1]) })

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

var connectedRe = regexp.MustCompile(`msg=connected pid=(\d+) db=(\S+)`)

// hostPIDs lists the pids of the hosts that said hello for database id, in
// the order of the log.
func hostPIDs(t *testing.T, e *Env, id string) []int {
	t.Helper()
	var pids []int
	for _, m := range connectedRe.FindAllStringSubmatch(hostLog(t, e), -1) {
		if m[2] == id {
			pid, _ := strconv.Atoi(m[1])
			pids = append(pids, pid)
		}
	}
	return pids
}

func hostLog(t *testing.T, e *Env) string {
	t.Helper()
	b, err := os.ReadFile(e.Paths.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
