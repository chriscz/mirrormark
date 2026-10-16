//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/firefox"
)

var bin = os.Getenv("MIRRORMARK_BIN")

func TestMain(m *testing.M) {
	if bin == "" {
		fmt.Fprintln(os.Stderr, "MIRRORMARK_BIN is not set; run: bin/dev run make check-e2e")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// Env is one test's world: a home directory and two browser instances, A
// and B, each with one profile bound to a database file.
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
	server         *exec.Cmd // the running mirrormark serve, or nil
	listens        int       // how many times a server of this test has listened
}

var extIDRe = regexp.MustCompile(`Extension ID: ([a-p]{32})`)

// kind is the kind of browser the instance called name is.
func (e *Env) kind(name string) string {
	if k, ok := e.kinds[name]; ok {
		return k
	}
	return "chrome"
}

// open loads the popup page of b's extension and attaches the tests to it.
// Firefox serves the extension under a UUID it picks per profile and
// records in prefs.js once the add-on is installed.
func (e *Env) open(t *testing.T, b *Browser) {
	t.Helper()
	if b.Kind == "firefox" {
		var uuid string
		eventually(t, 15*time.Second, b.Name+"'s add-on UUID in prefs.js", func() bool {
			uuid, _ = firefox.UUID(b.UDD)
			return uuid != ""
		})
		b.open(t, "moz-extension://"+uuid+"/popup.html")
		return
	}
	b.open(t, "chrome-extension://"+e.ExtID+"/popup.html")
}

// setup is two Chrome instances.
func setup(t *testing.T) *Env { return setupPair(t, "chrome", "chrome") }

// setupPair installs and binds as a user would, with profile a in browser
// kind ka and b in kb, "chrome" or "firefox": each browser runs once (so
// its profile exists), install writes the files and asks for a restart,
// the browsers restart, and install runs again for each profile to bind.
// Two Firefox profiles share one profiles directory, as a user's would,
// and run as two Firefox processes.
func setupPair(t *testing.T, ka, kb string) *Env {
	e := newEnv(t)
	e.kinds = map[string]string{"a": ka, "b": kb}
	e.firstInstall(t)
	e.A, e.B = e.launch("a"), e.launch("b")
	e.DBA, e.DBB = e.bind("a"), e.bind("b")
	e.open(t, e.A)
	e.open(t, e.B)
	e.LabelA, e.LabelB = e.label(e.DBA), e.label(e.DBB)
	return e
}

// newEnv makes the test's home directory and the environment that the
// browsers and mirrormark run with; none sees the real home.
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
	e.serve(t)
	return e
}

// serve starts the test's mirrormark server, as the user's systemd service
// would; the dev image has no user session for systemd. It stops when the
// test ends, before the home directory goes.
func (e *Env) serve(t *testing.T) {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Env = e.env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	e.server = cmd
	t.Cleanup(func() { e.stopServer(t) })
	eventually(t, 5*time.Second, "the server to listen", func() bool {
		b, _ := os.ReadFile(e.Paths.LogFile())
		return bytes.Count(b, []byte("msg=listening")) > e.listens
	})
	e.listens++
}

// stopServer stops the test's server, as a crash or systemctl stop would.
func (e *Env) stopServer(t *testing.T) {
	if e.server == nil {
		return
	}
	e.server.Process.Signal(syscall.SIGTERM)
	e.server.Wait()
	e.server = nil
}

// firstInstall prepares both profiles and runs install's first pass,
// which writes the files and asks for a restart: once for every Chrome
// data directory, since it writes Chrome's system files, and once per
// Firefox profile, since each holds its own add-on and prefs.
func (e *Env) firstInstall(t *testing.T) {
	t.Helper()
	e.prepare(t, "a")
	e.prepare(t, "b")
	if e.kind("a") == "chrome" || e.kind("b") == "chrome" {
		// Registered before install runs, so an install that fails part-way
		// leaves nothing behind either, and run before t.TempDir's cleanup:
		// later tests must not see this test's extension in the shared
		// system directories.
		t.Cleanup(func() { e.mm(t, "uninstall") })
	}
	chromeDone := false
	for _, name := range []string{"a", "b"} {
		restart := "Restart Firefox"
		if e.kind(name) == "chrome" {
			if chromeDone {
				continue // one install writes the system files for every data directory
			}
			chromeDone = true
			restart = "Restart Chrome"
		}
		out := e.mm(t, e.installArgs(name)...)
		if !strings.Contains(out, restart) {
			t.Fatalf("first install did not ask for a restart:\n%s", out)
		}
		if e.kind(name) == "chrome" {
			m := extIDRe.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("first install did not print the extension id:\n%s", out)
			}
			e.ExtID = m[1]
		}
	}
}

// prepare makes the profile exist as a user's would after using the
// browser once: Chrome's data directory with its profile list, or a Firefox
// profile in profiles.ini with its compatibility.ini.
func (e *Env) prepare(t *testing.T, name string) {
	t.Helper()
	if e.kind(name) == "firefox" {
		dir := e.udd(name)
		cmd := exec.Command(firefoxBin, "--headless", "-CreateProfile", name+" "+dir)
		cmd.Env = e.env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create Firefox profile %s: %v\n%s", name, err, out)
		}
		// The remote agent sets "recommended" prefs for automation when a
		// session starts; they could hide whether install's prefs work.
		if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte("user_pref(\"remote.prefs.recommended\", false);\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.launch(name).Close()
}

func (e *Env) udd(name string) string {
	if e.kind(name) == "firefox" {
		return filepath.Join(e.Home, "firefox", name)
	}
	return filepath.Join(e.Home, "chrome-"+name)
}

func (e *Env) launch(name string) *Browser {
	if e.kind(name) == "firefox" {
		return launchFirefox(e.t, name, e.udd(name), map[string]int{"a": 9301, "b": 9302}[name], e.env)
	}
	return launch(e.t, name, e.udd(name), e.env)
}

// installArgs are the arguments of an install into name's profile.
func (e *Env) installArgs(name string) []string {
	if e.kind(name) == "firefox" {
		return []string{"install", "--browser", "firefox", "--profile", name}
	}
	return []string{"install", "--user-data-dir", e.udd(name), "--profile", "Default"}
}

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

// bind waits until name's profile is ready, then runs install for it and
// returns the database it bound to. install names every profile by its
// ID: a Chrome profile's directory, Default, or the Firefox profile's
// directory, a.
func (e *Env) bind(name string) string {
	e.ready(name)
	out := e.mm(e.t, e.installArgs(name)...)
	profile := "Default"
	if e.kind(name) == "firefox" {
		profile = name
	}
	m := regexp.MustCompile(`bound: ` + regexp.QuoteMeta(profile) + ` -> ([a-z0-9-]+)`).FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("install did not bind %s:\n%s", name, out)
	}
	return m[1]
}

// ready waits until name's profile files say mirrormark is ready there,
// which is what install checks before it opens the bind page. Chromium
// writes Preferences up to about 20 seconds after it installs the
// extension; Gecko within a second or two.
func (e *Env) ready(name string) {
	e.t.Helper()
	want, err := extension.Version()
	if err != nil {
		e.t.Fatal(err)
	}
	eventually(e.t, 40*time.Second, name+"'s files to show mirrormark ready", func() bool {
		if e.kind(name) == "firefox" {
			s, _, err := firefox.ReadState(e.udd(name), want)
			return err == nil && s.State == chrome.Ready
		}
		s, err := chrome.ReadState(filepath.Join(e.udd(name), "Default"), e.ExtID, want, true)
		return err == nil && s.State == chrome.Ready
	})
}

// mmExit runs mirrormark like mm, but returns a failing run's output and
// exit code instead of failing the test.
func (e *Env) mmExit(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env, cmd.Stdin = e.env, strings.NewReader("")
	out, err := cmd.CombinedOutput()
	t.Logf("mirrormark %s:\n%s", strings.Join(args, " "), out)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("mirrormark %v: %v", args, err)
	}
	return string(out), 0
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
