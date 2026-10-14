//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
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
	eval[bool](t, e.A, `chrome.storage.local.remove("status").then(() => true)`)
	if out := e.mm(t, "install", "--user-data-dir", e.A.UDD); !strings.Contains(out, "bound: Default -> "+e.DBA) {
		t.Fatalf("second install rebound to another database:\n%s", out)
	}
	roundDone(t, e.A, "a round after binding again")
	log := hostLog(t, e)
	if n := strings.Count(log, "msg=connected pid="); n != 3 {
		t.Errorf("want 3 hellos in the host log (a, b, a again), got %d", n)
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

// TestBindStartsChrome follows a user who closes Chrome as the first pass
// asks and runs install again without opening it: install starts Chrome
// itself. That is the profile's first start with the extension, when the
// first bind tab can load before the extension and Bind has to retry.
func TestBindStartsChrome(t *testing.T) {
	e := newEnv(t)
	e.firstInstall(t)
	e.noSandbox(t)
	t.Cleanup(func() { stopChrome(t, e.udd("a")) })
	start := time.Now()
	id := e.bind("a")
	// Not asserted: whether the first tab loses the race is up to Chrome.
	t.Logf("bound in %v; retried: %v", time.Since(start).Round(time.Millisecond), time.Since(start) > chrome.BindWait)
	stopChrome(t, e.udd("a"))

	e.A = e.launch("a")
	e.A.open(t, e.ExtID)
	if got := eval[string](t, e.A, `chrome.storage.local.get("db").then((r) => r.db)`); got != id {
		t.Errorf("a is bound to %q, want %q", got, id)
	}
	roundDone(t, e.A, "a round after Chrome restarted")
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
// start takes it over, so stopChrome waits for the process instead. The
// process is not this test's child; until something reaps it, it lingers
// as a zombie, which has exited all the same.
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
	exited := func() bool {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		// The state follows the parenthesised command name.
		return err != nil || strings.HasPrefix(string(stat[bytes.LastIndexByte(stat, ')')+1:]), " Z")
	}
	if exited() {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("stop Chrome %d: %v", pid, err)
	}
	eventually(t, 20*time.Second, "Chrome to exit", exited)
}

func hostLog(t *testing.T, e *Env) string {
	t.Helper()
	b, err := os.ReadFile(e.Paths.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
