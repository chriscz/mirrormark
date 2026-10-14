package host_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/host"
	"github.com/chriscz/mirrormark/internal/merge"
)

// ext is a fake extension: it drives a real host.Run over pipes and applies
// its ops to a fakebrowser.Browser, as the extension does in Chrome.
type ext struct {
	t       *testing.T
	mu      sync.Mutex
	b       *fakebrowser.Browser
	w       *io.PipeWriter
	o       extOpts
	errs    []host.Msg     // errors, and the problems in status messages
	status  []host.Problem // the last status message's problems
	rounds  int            // status messages, one per round
	applies int
	done    chan error
}

type extOpts struct {
	silent  bool                // never answer apply
	short   bool                // answer apply with one result too few
	fail    func(merge.Op) bool // make these ops fail in the browser
	poll    time.Duration       // the host's poll interval; default 20 ms
	real    bool                // use the host's default timings
	log     *logBuf             // the host's log, at debug level; default discarded
	noHello bool                // send neither hello nor the first tree
}

// logBuf is a log the test reads while the host writes it.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func paths(t *testing.T) db.Paths {
	d := t.TempDir()
	return db.Paths{Config: d + "/config", Data: d + "/data", State: d + "/state"}
}

func addProfile(t *testing.T, p db.Paths, id string) {
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: "chrome/" + id, Browser: "chrome"}}
	if err := db.SaveDB(p.ProfileFile(id), d); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, p db.Paths, text string) {
	if err := db.WriteAtomic(p.ConfigFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

const workUnion = "format: 1\nunions:\n  work:\n    a: toolbar/Work\n    b: toolbar/Work\n"

// start connects a fake extension bound to dbID and sends hello and a tree.
func start(t *testing.T, p db.Paths, dbID string, b *fakebrowser.Browser, o extOpts) *ext {
	hr, ew := io.Pipe()
	er, hw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	e := &ext{t: t, b: b, w: ew, o: o, done: make(chan error, 1)}
	ho := host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if o.log != nil {
		ho.Log = slog.New(slog.NewTextHandler(o.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	if !o.real {
		ho.Timeout, ho.Poll = 300*time.Millisecond, 20*time.Millisecond
		if o.poll != 0 {
			ho.Poll = o.poll
		}
	}
	exited := make(chan struct{})
	go func() {
		e.done <- host.Run(ctx, hr, hw, ho)
		hw.Close()
		close(exited)
	}()
	// Wait for the host, so it does not write into a removed temp dir.
	t.Cleanup(func() { cancel(); ew.Close(); <-exited })
	go func() {
		for {
			m, err := host.ReadMsg(er)
			if err != nil {
				return
			}
			switch m.Type {
			case "apply":
				e.mu.Lock()
				e.applies++
				e.mu.Unlock()
				if e.o.silent {
					continue
				}
				e.mu.Lock()
				res := e.b.Apply(m.Ops, func(i int) bool { return e.o.fail != nil && e.o.fail(m.Ops[i]) })
				e.mu.Unlock()
				if e.o.short {
					res = res[:len(res)-1]
				}
				e.send(host.Msg{V: 1, Type: "applied", Batch: m.Batch, Results: res})
				e.sendTree()
			case "error":
				e.mu.Lock()
				e.errs = append(e.errs, m)
				e.mu.Unlock()
			case "status":
				e.mu.Lock()
				for _, p := range m.Problems {
					e.errs = append(e.errs, host.Msg{Type: "status", Code: p.Code, Message: p.Message})
				}
				e.status = m.Problems
				e.rounds++
				e.mu.Unlock()
			}
		}
	}()
	if !o.noHello {
		e.send(host.Msg{V: 1, Type: "hello", DB: dbID, Browser: "chrome/test"})
		e.sendTree()
	}
	return e
}

func (e *ext) send(m host.Msg) {
	if err := host.WriteMsg(e.w, m); err != nil {
		e.t.Log("send:", err)
	}
}

func (e *ext) sendTree() {
	e.mu.Lock()
	nodes := e.b.Tree()
	e.mu.Unlock()
	e.send(host.Msg{V: 1, Type: "tree", Nodes: nodes})
}

// edit changes the browser as a user would, then reports the new tree.
func (e *ext) edit(f func(b *fakebrowser.Browser)) {
	e.mu.Lock()
	f(e.b)
	e.mu.Unlock()
	e.sendTree()
}

func (e *ext) has(folder string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.b.Find("1", folder) != ""
}

func (e *ext) lines(folder string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.b.Find("1", folder)
	if id == "" {
		return nil
	}
	return e.b.Lines(id)
}

// errors returns the error messages the host sent so far.
func (e *ext) errors() []host.Msg {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.errs)
}

func (e *ext) hasError(code string) bool {
	return slices.ContainsFunc(e.errors(), func(m host.Msg) bool { return m.Code == code })
}

func (e *ext) count() (applies, rounds int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applies, e.rounds
}

// healthy reports whether the last round found no problem.
func (e *ext) healthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rounds > 0 && len(e.status) == 0
}

func (e *ext) firstError() host.Msg {
	eventually(e.t, "an error", func() bool { return len(e.errors()) > 0 })
	return e.errors()[0]
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTwoHostsSyncAUnion(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	a := start(t, p, "a", fakebrowser.New(), extOpts{})
	b := start(t, p, "b", fakebrowser.New(), extOpts{})
	eventually(t, "member folders", func() bool { return a.has("Work") && b.has("Work") })

	a.edit(func(br *fakebrowser.Browser) { br.Add(br.Find("1", "Work"), "GitHub", "https://github.com/") })
	want := []string{"/GitHub https://github.com/"}
	eventually(t, "add to reach b", func() bool { return slices.Equal(b.lines("Work"), want) })

	b.edit(func(br *fakebrowser.Browser) { br.Remove(br.Find(br.Find("1", "Work"), "GitHub")) })
	want = []string{"/_deleted/", "/_deleted/GitHub https://github.com/"}
	eventually(t, "delete to reach a", func() bool { return slices.Equal(a.lines("Work"), want) })

	// Quiet: no more writes.
	time.Sleep(200 * time.Millisecond)
	before, _ := os.Stat(p.ProfileFile("a"))
	time.Sleep(300 * time.Millisecond)
	after, _ := os.Stat(p.ProfileFile("a"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("a's database file is still being written")
	}
	if !db.Connected(p, "a", time.Time{}) {
		t.Fatal("a's hello did not touch its lock file")
	}
	if errs := append(a.errors(), b.errors()...); len(errs) > 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

// With the host's real timings an edit reaches the other profile within the
// 3 s target, less the extension's 0.5 s quiet time the fake does not wait.
func TestChangeReachesOtherProfileQuickly(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	a := start(t, p, "a", fakebrowser.New(), extOpts{real: true})
	b := start(t, p, "b", fakebrowser.New(), extOpts{real: true})
	eventually(t, "member folders", func() bool { return a.has("Work") && b.has("Work") })
	time.Sleep(1500 * time.Millisecond) // let the first rounds settle

	began := time.Now()
	a.edit(func(br *fakebrowser.Browser) { br.Add(br.Find("1", "Work"), "GitHub", "https://github.com/") })
	eventually(t, "add to reach b", func() bool { return slices.Equal(b.lines("Work"), []string{"/GitHub https://github.com/"}) })
	d := time.Since(began)
	t.Logf("edit reached b after %v", d)
	if d > 2500*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestUnknownDatabaseIsRefused(t *testing.T) {
	e := start(t, paths(t), "nope", fakebrowser.New(), extOpts{})
	if m := e.firstError(); m.Code != "db-missing" {
		t.Fatalf("error %+v", m)
	}
	if err := <-e.done; err == nil {
		t.Fatal("host kept running")
	}
}

func TestSecondHostForOneDatabaseIsRefused(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	start(t, p, "a", fakebrowser.New(), extOpts{})
	time.Sleep(50 * time.Millisecond)
	e := start(t, p, "a", fakebrowser.New(), extOpts{})
	if m := e.firstError(); m.Code != "db-locked" {
		t.Fatalf("error %+v", m)
	}
}

// A lock file that cannot be locked at all is not another host holding it.
func TestUnusableLockFileIsALockError(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	if err := os.Mkdir(p.ProfileLock("a"), 0o700); err != nil {
		t.Fatal(err)
	}
	e := start(t, p, "a", fakebrowser.New(), extOpts{})
	if m := e.firstError(); m.Code != "db-lock-error" || m.Type != "error" {
		t.Fatalf("error %+v", m)
	}
	if err := <-e.done; err == nil {
		t.Fatal("host kept running")
	}
}

func TestOtherVersionIsRefused(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	hr, ew := io.Pipe()
	er, hw := io.Pipe()
	go host.Run(context.Background(), hr, hw, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go host.WriteMsg(ew, host.Msg{V: 2, Type: "hello", DB: "a"})
	m, err := host.ReadMsg(er)
	if err != nil || m.Code != "unsupported-version" {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestSilentBrowserWritesNothing(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	bb := fakebrowser.New()
	bb.Add(bb.Add("1", "Work", ""), "X", "https://x/")
	start(t, p, "b", bb, extOpts{})
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{silent: true})
	eventually(t, "the timeout to be reported", func() bool { return a.hasError("apply-failed") })
	d, err := db.LoadDB(p.ProfileFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Unions["work"] != nil {
		t.Fatalf("a wrote a record without its browser applying the batch: %+v", d.Unions["work"])
	}
}

// An op that fails every time writes no file and shows in the popup. It is
// retried at the poll, not as fast as the browser answers, and after three
// failures in a row only every few seconds. The log gets its warnings once.
// A round that succeeds clears the popup and the back-off.
func TestFailingOpBacksOffAndRecovers(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	bb := fakebrowser.New()
	bw := bb.Add("1", "Work", "")
	bb.Add(bw, "X", "https://x/")
	bb.Add(bw, "Y", "https://y/")
	b := start(t, p, "b", bb, extOpts{})
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	// One of the creates lands, the others fail while failing is set.
	var failing atomic.Bool
	failing.Store(true)
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	log := &logBuf{}
	a := start(t, p, "a", ab, extOpts{log: log, fail: func(op merge.Op) bool {
		return failing.Load() && op.Op == "create" && op.Title != nil && (*op.Title == "Y" || *op.Title == "Z")
	}})
	m := a.firstError()
	if m.Code != "apply-failed" || !strings.Contains(m.Message, "injected") {
		t.Fatalf("error %+v", m)
	}
	eventually(t, "three tries", func() bool { n, _ := a.count(); return n >= 3 })
	time.Sleep(500 * time.Millisecond) // 25 polls
	if n, _ := a.count(); n != 3 {
		t.Fatalf("%d batches; want 3 before the 5 s back-off", n)
	}
	d, err := db.LoadDB(p.ProfileFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Unions["work"] != nil {
		t.Fatalf("a wrote a record although an op failed: %+v", d.Unions["work"])
	}
	if n := strings.Count(log.String(), "msg=\"op failed\""); n != 1 {
		t.Fatalf("op failed logged %d times:\n%s", n, log)
	}

	// A changed file starts a round despite the back-off; this one succeeds.
	failing.Store(false)
	writeConfig(t, p, workUnion+"\n")
	eventually(t, "a clean round", a.healthy)
	if !strings.Contains(log.String(), "warning stopped repeating") {
		t.Fatalf("the log does not say how often the warning repeated:\n%s", log)
	}

	// The back-off starts over: three quick tries again.
	failing.Store(true)
	before, _ := a.count()
	b.edit(func(br *fakebrowser.Browser) { br.Add(br.Find("1", "Work"), "Z", "https://z/") })
	eventually(t, "three quick tries", func() bool { n, _ := a.count(); return n >= before+3 })
}

func TestRetryDelay(t *testing.T) {
	want := []time.Duration{0, 0, 0, 5 * time.Second, 5 * time.Second, 5 * time.Second, 30 * time.Second, 30 * time.Second}
	for f, w := range want {
		if got := host.RetryDelay(f); got != w {
			t.Errorf("after %d failures: %v, want %v", f, got, w)
		}
	}
}

// The popup shows a problem until a round finds none.
func TestProblemStaysUntilFixed(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n    c: toolbar/Work\n")
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{})
	eventually(t, "union-skipped", func() bool { return a.hasError("union-skipped") })
	a.sendTree() // another round, still skipped
	_, rounds := a.count()
	eventually(t, "the next round", func() bool { _, r := a.count(); return r > rounds })
	if a.healthy() {
		t.Fatal("a round that still skips the union reported no problem")
	}
	addProfile(t, p, "c")
	eventually(t, "a clean round", a.healthy)
}

// The poll notices a file whose size changed although its mtime did not.
func TestPollNoticesSizeChange(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n")
	a := start(t, p, "a", fakebrowser.New(), extOpts{})
	eventually(t, "a's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("a"))
		return err == nil && d.Unions["work"] != nil
	})
	info, err := os.Stat(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	// Swap the file in whole, so the poll never sees a new mtime.
	tmp := p.ConfigFile() + ".new"
	if err := os.WriteFile(tmp, []byte("format: 1\nunions: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "config-invalid", func() bool { return a.hasError("config-invalid") })
}

// When the database file cannot be written after the browser made the
// round's changes, the popup says so and the round is retried.
func TestDatabaseWriteFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	bb := fakebrowser.New()
	bb.Add(bb.Add("1", "Work", ""), "X", "https://x/")
	start(t, p, "b", bb, extOpts{})
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	// a's hello needs its lock file, which a read-only directory cannot gain.
	if err := os.WriteFile(p.ProfileLock("a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.ProfilesDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p.ProfilesDir(), 0o700) })
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{})
	eventually(t, "db-write-failed", func() bool { return a.hasError("db-write-failed") })
	if a.hasError("apply-failed") {
		t.Fatalf("reported as apply-failed too: %+v", a.errors())
	}
	if err := os.Chmod(p.ProfilesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, p, workUnion+"\n") // a round now, whatever the back-off
	eventually(t, "a's write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("a"))
		return err == nil && d.Unions["work"] != nil
	})
}

func TestUnreadableMemberSkipsTheUnion(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n    c: toolbar/Work\n")
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{})
	m := a.firstError()
	if m.Code != "union-skipped" || !strings.Contains(m.Message, "work") || !strings.Contains(m.Message, "member c") {
		t.Fatalf("error %+v", m)
	}
}

// The wrong-tree guard is reported once, as db-mismatch, not also as a
// skipped union.
func TestWrongTreeIsReportedOnce(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	ab := fakebrowser.New()
	work := ab.Add("1", "Work", "")
	for _, s := range []string{"1", "2", "3", "4", "5", "6"} {
		ab.Add(work, "B"+s, "https://b/"+s)
	}
	a := start(t, p, "a", ab, extOpts{})
	b := start(t, p, "b", fakebrowser.New(), extOpts{})
	eventually(t, "b to get the bookmarks", func() bool { return len(b.lines("Work")) == 6 })
	a.edit(func(br *fakebrowser.Browser) {
		w := br.Find("1", "Work")
		for _, n := range br.Children(w) {
			br.Remove(n.ID)
		}
	})
	eventually(t, "db-mismatch", func() bool { return a.hasError("db-mismatch") })
	if a.hasError("union-skipped") {
		t.Fatalf("db-mismatch also reported as union-skipped: %+v", a.errors())
	}
	if m := a.errors()[0]; !strings.Contains(m.Message, "work") {
		t.Fatalf("message does not name the union: %+v", m)
	}
}

// An applied message with too few results is a failed batch.
func TestShortAnswerWritesNothing(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	bb := fakebrowser.New()
	bb.Add(bb.Add("1", "Work", ""), "X", "https://x/")
	start(t, p, "b", bb, extOpts{})
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{short: true})
	m := a.firstError()
	if m.Code != "apply-failed" || !strings.Contains(m.Message, "answered 0 of 1") {
		t.Fatalf("error %+v", m)
	}
	d, err := db.LoadDB(p.ProfileFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Unions["work"] != nil {
		t.Fatalf("a wrote a record from a short answer: %+v", d.Unions["work"])
	}
}

// When the browser closes the port the host returns nil and frees the
// profile for the next connection.
func TestEOFStopsCleanlyAndFreesTheLock(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	e := start(t, p, "a", fakebrowser.New(), extOpts{})
	eventually(t, "hello", func() bool { return db.Connected(p, "a", time.Time{}) })
	e.w.Close()
	select {
	case err := <-e.done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop")
	}
	unlock, err := db.TryLock(p.ProfileLock("a"))
	if err != nil {
		t.Fatalf("profile lock still held: %v", err)
	}
	unlock()
}

// A message of an unknown type is ignored; one that is not JSON stops the
// host with an error and frees the profile.
func TestMalformedMessages(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	e := start(t, p, "a", fakebrowser.New(), extOpts{})
	eventually(t, "hello", func() bool { return db.Connected(p, "a", time.Time{}) })
	e.send(host.Msg{V: 1, Type: "bogus"})
	select {
	case err := <-e.done:
		t.Fatalf("host stopped on an unknown type: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	frame := binary.NativeEndian.AppendUint32(nil, 5)
	if _, err := e.w.Write(append(frame, "{oops"...)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-e.done:
		if err == nil || !strings.Contains(err.Error(), "bad message") {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop")
	}
	unlock, err := db.TryLock(p.ProfileLock("a"))
	if err != nil {
		t.Fatalf("profile lock still held: %v", err)
	}
	unlock()
}

// A tree that comes before hello has no profile to sync; it is ignored.
func TestTreeBeforeHelloIsIgnored(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n")
	log := &logBuf{}
	e := start(t, p, "a", fakebrowser.New(), extOpts{log: log, noHello: true})
	e.sendTree()
	eventually(t, "the debug line", func() bool {
		return strings.Contains(log.String(), `level=DEBUG msg="tree before hello ignored"`)
	})
	if applies, rounds := e.count(); applies != 0 || rounds != 0 {
		t.Fatalf("%d applies and %d rounds before hello", applies, rounds)
	}
	e.send(host.Msg{V: 1, Type: "hello", DB: "a", Browser: "chrome/test"})
	e.sendTree()
	eventually(t, "the member folder", func() bool { return e.has("Work") })
}

// Rounds never overlap: a host waits for the global lock before it reads
// files or sends ops.
func TestRoundWaitsForTheGlobalLock(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n")
	unlock, err := db.Lock(p.GlobalLock())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	e := start(t, p, "a", fakebrowser.New(), extOpts{})
	t.Cleanup(release) // before the host's cleanup waits for it to stop
	eventually(t, "hello", func() bool { return db.Connected(p, "a", time.Time{}) })
	time.Sleep(300 * time.Millisecond)
	if applies, rounds := e.count(); applies != 0 || rounds != 0 {
		t.Fatalf("%d applies and %d rounds while another round held the lock", applies, rounds)
	}
	release()
	eventually(t, "the member folder", func() bool { return e.has("Work") })
}
