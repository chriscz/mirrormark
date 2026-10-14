package host_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
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
	errs    []host.Msg
	applies int
	done    chan error
}

type extOpts struct {
	silent bool                // never answer apply
	fail   func(merge.Op) bool // make these ops fail in the browser
	poll   time.Duration       // the host's poll interval; default 20 ms
	real   bool                // use the host's default timings
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
	if !o.real {
		ho.Timeout, ho.Poll = 300*time.Millisecond, 20*time.Millisecond
		if o.poll != 0 {
			ho.Poll = o.poll
		}
	}
	go func() {
		e.done <- host.Run(ctx, hr, hw, ho)
		hw.Close()
	}()
	t.Cleanup(func() { cancel(); ew.Close() })
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
				e.send(host.Msg{V: 1, Type: "applied", Batch: m.Batch, Results: res})
				e.sendTree()
			case "error":
				e.mu.Lock()
				e.errs = append(e.errs, m)
				e.mu.Unlock()
			}
		}
	}()
	e.send(host.Msg{V: 1, Type: "hello", DB: dbID, Browser: "chrome/test"})
	e.sendTree()
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

// An op that fails every time writes no file, shows in the popup, and is
// retried at the poll interval rather than as fast as the browser answers.
func TestFailingOpIsReportedAndNotRetriedHot(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, workUnion)
	bb := fakebrowser.New()
	bw := bb.Add("1", "Work", "")
	bb.Add(bw, "X", "https://x/")
	bb.Add(bw, "Y", "https://y/")
	start(t, p, "b", bb, extOpts{})
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	// One of the two creates lands, the other always fails.
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	a := start(t, p, "a", ab, extOpts{poll: 100 * time.Millisecond, fail: func(op merge.Op) bool {
		return op.Op == "create" && op.Title != nil && *op.Title == "Y"
	}})
	m := a.firstError()
	if m.Code != "apply-failed" || !strings.Contains(m.Message, "injected") {
		t.Fatalf("error %+v", m)
	}
	a.mu.Lock()
	a.applies = 0
	a.mu.Unlock()
	time.Sleep(time.Second)
	a.mu.Lock()
	n := a.applies
	a.mu.Unlock()
	if n < 3 || n > 15 {
		t.Fatalf("%d batches in 1 s with a 100 ms poll", n)
	}
	d, err := db.LoadDB(p.ProfileFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Unions["work"] != nil {
		t.Fatalf("a wrote a record although an op failed: %+v", d.Unions["work"])
	}
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
