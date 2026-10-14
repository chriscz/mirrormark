//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
)

var bin = os.Getenv("MIRRORMARK_BIN")

func TestMain(m *testing.M) {
	if bin == "" {
		fmt.Fprintln(os.Stderr, "MIRRORMARK_BIN is not set; run: bin/dev run make check-e2e")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// Env is one test's world: a home directory, two Chrome instances with
// their Default profiles bound to two database files.
type Env struct {
	t              *testing.T
	Home           string
	Paths          db.Paths
	env            []string
	ExtID          string
	kinds          map[string]string // browser name to "chrome" or "firefox"
	A, B           *Browser
	DBA, DBB       string
	LabelA, LabelB string
}

var (
	extIDRe = regexp.MustCompile(`Extension ID: ([a-p]{32})`)
	boundRe = regexp.MustCompile(`bound: Default -> ([a-z0-9-]+)`)
)

// kind is the kind of browser the instance called name is.
func (e *Env) kind(name string) string {
	if k, ok := e.kinds[name]; ok {
		return k
	}
	return "chrome"
}

// open loads the popup page of b's extension and attaches the tests to it.
func (e *Env) open(t *testing.T, b *Browser) {
	t.Helper()
	b.open(t, "chrome-extension://"+e.ExtID+"/popup.html")
}

// setup installs and binds as a user would: Chrome runs once (so its
// profile list exists), install writes the files and asks for a restart,
// Chrome restarts, and install runs again for each data directory to bind.
func setup(t *testing.T) *Env {
	e := newEnv(t)
	e.firstInstall(t)
	e.A, e.B = e.launch("a"), e.launch("b")
	e.DBA, e.DBB = e.bind("a"), e.bind("b")
	e.open(t, e.A)
	e.open(t, e.B)
	e.LabelA, e.LabelB = e.label(e.DBA), e.label(e.DBB)
	return e
}

// newEnv makes the test's home directory and the environment that Chrome
// and mirrormark run with; neither sees the real home.
func newEnv(t *testing.T) *Env {
	home := t.TempDir()
	e := &Env{t: t, Home: home, Paths: db.Paths{
		Config: filepath.Join(home, ".config", "mirrormark"),
		Data:   filepath.Join(home, ".local", "share", "mirrormark"),
		State:  filepath.Join(home, ".local", "state", "mirrormark"),
	}}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "XDG_") {
			e.env = append(e.env, kv)
		}
	}
	e.env = append(e.env, "HOME="+home)
	return e
}

// firstInstall starts and closes both Chrome instances, so their profile
// lists exist, and runs install's first pass, which writes the system
// files and asks for a restart.
func (e *Env) firstInstall(t *testing.T) {
	t.Helper()
	e.launch("a").Close()
	e.launch("b").Close()
	// Registered before install runs, so an install that fails part-way
	// leaves nothing behind either, and run before t.TempDir's cleanup:
	// later tests must not see this test's extension in the shared system
	// directories.
	t.Cleanup(func() { e.mm(t, "uninstall") })
	out := e.mm(t, "install", "--user-data-dir", e.udd("a"))
	m := extIDRe.FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "Restart Chrome") {
		t.Fatalf("first install did not ask for a restart:\n%s", out)
	}
	e.ExtID = m[1]
}

func (e *Env) udd(name string) string      { return filepath.Join(e.Home, "chrome-"+name) }
func (e *Env) launch(name string) *Browser { return launch(e.t, name, e.udd(name), e.env) }

// mm runs the mirrormark binary as the test's user.
func (e *Env) mm(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env, cmd.Stdin = e.env, strings.NewReader("")
	start := time.Now()
	out, err := cmd.CombinedOutput()
	t.Logf("mirrormark %s (%v):\n%s", strings.Join(args, " "), time.Since(start).Round(time.Millisecond), out)
	if err != nil {
		t.Fatalf("mirrormark %v: %v", args, err)
	}
	return string(out)
}

func (e *Env) bind(name string) string {
	out := e.mm(e.t, "install", "--user-data-dir", e.udd(name))
	m := boundRe.FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("install did not bind %s:\n%s", name, out)
	}
	return m[1]
}

func (e *Env) label(id string) string {
	d, err := db.LoadDB(e.Paths.ProfileFile(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Profile.Label
}

// Union shares toolbar/Work of both profiles as union "work" and waits for
// the first rounds to create the folders.
func (e *Env) Union(t *testing.T) {
	t.Helper()
	e.Share(t)
	eventually(t, 10*time.Second, "member folders", func() bool {
		return e.A.Lines(t, "toolbar/Work") != nil && e.B.Lines(t, "toolbar/Work") != nil
	})
}

// Share writes the config that shares toolbar/Work of both profiles as
// union "work".
func (e *Env) Share(t *testing.T) {
	t.Helper()
	cfg := fmt.Sprintf("format: 1\nunions:\n  work:\n    %s: toolbar/Work\n    %s: toolbar/Work\n", e.DBA, e.DBB)
	if err := db.WriteAtomic(e.Paths.ConfigFile(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", within, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitLines waits until the folder at path in b holds want and returns how
// long it took.
func waitLines(t *testing.T, b *Browser, path string, want []string, within time.Duration) time.Duration {
	t.Helper()
	want = slices.Sorted(slices.Values(want))
	start := time.Now()
	var got []string
	for time.Since(start) < within {
		if got = b.Lines(t, path); slices.Equal(got, want) {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: %s after %v:\n got %q\nwant %q", b.Name, path, within, got, want)
	return 0
}
