//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
)

const (
	work = "toolbar/Work" // the union's member folder in both profiles
	gh   = "https://github.com/"
	lwn  = "https://lwn.net/"
)

// syncTimeout is generous; the latency target is checked by latency.
const syncTimeout = 10 * time.Second

// target is how soon an edit in one running profile must show in the other.
const latencyTarget = 3 * time.Second

// synced shares Work and puts one bookmark in A's, then waits for it in B.
func synced(t *testing.T) *Env {
	t.Helper()
	e := setup(t)
	e.Union(t)
	e.A.Add(t, work, "GitHub", gh)
	waitLines(t, e.B, work, []string{"/GitHub " + gh}, syncTimeout)
	return e
}

// latency waits for an edit made just before to reach b, logs how long it
// took and fails the test if that is longer than the target.
func latency(t *testing.T, b *Browser, what string, want ...string) {
	t.Helper()
	d := waitLines(t, b, work, want, syncTimeout)
	t.Logf("latency: %s reached %s after %v", what, b.Name, d.Round(time.Millisecond))
	if d > latencyTarget {
		t.Errorf("%s reached %s after %v; the target is %v", what, b.Name, d, latencyTarget)
	}
}

// both waits until both member folders hold want.
func both(t *testing.T, e *Env, want ...string) {
	t.Helper()
	waitLines(t, e.A, work, want, syncTimeout)
	waitLines(t, e.B, work, want, syncTimeout)
}

// settled ends a test: both member folders and both database records hold
// want, and the host log has no warning or error other than those matching
// expected. It waits for the database records, so that the rounds after the
// last edit have finished before the log is read.
func settled(t *testing.T, e *Env, expected *regexp.Regexp, want ...string) {
	t.Helper()
	both(t, e, want...)
	want = slices.Sorted(slices.Values(want))
	for _, id := range []string{e.DBA, e.DBB} {
		var got []string
		eventually(t, syncTimeout, id+" to record the union", func() bool {
			got = recorded(t, e, id)
			return slices.Equal(got, want)
		})
	}
	for line := range strings.Lines(hostLog(t, e)) {
		if strings.Contains(line, "db-mismatch") {
			t.Errorf("the wrong-tree guard tripped: %s", line)
		} else if (strings.Contains(line, "level=WARN") || strings.Contains(line, "level=ERROR")) &&
			(expected == nil || !expected.MatchString(line)) {
			t.Errorf("unexpected in the host log: %s", line)
		}
	}
}

// recorded lists the union as profile id's database file records it.
func recorded(t *testing.T, e *Env, id string) []string {
	t.Helper()
	d, err := db.LoadDB(e.Paths.ProfileFile(id))
	if err != nil || d.Unions["work"] == nil {
		return nil
	}
	return fakebrowser.NodeLines(d.Unions["work"].Children)
}

// TestEditsPropagate makes each kind of edit in one browser and checks that
// it reaches the other within the target.
func TestEditsPropagate(t *testing.T) {
	e := synced(t)
	e.B.Add(t, work, "News", lwn)
	latency(t, e.A, "add", "/GitHub "+gh, "/News "+lwn)
	e.A.Rename(t, work+"/GitHub "+gh, "GH")
	latency(t, e.B, "rename", "/GH "+gh, "/News "+lwn)
	e.B.SetURL(t, work+"/GH "+gh, gh+"chriscz")
	latency(t, e.A, "set URL", "/GH "+gh+"chriscz", "/News "+lwn)
	e.A.Mkdir(t, work+"/Dev")
	latency(t, e.B, "new folder", "/Dev/", "/GH "+gh+"chriscz", "/News "+lwn)
	e.B.Move(t, work+"/GH "+gh+"chriscz", work+"/Dev", 0)
	latency(t, e.A, "move into a folder", "/Dev/", "/Dev/GH "+gh+"chriscz", "/News "+lwn)
	e.A.Mkdir(t, work+"/Dev/Go")
	e.A.Move(t, work+"/News "+lwn, work+"/Dev/Go", -1)
	latency(t, e.B, "new subfolder and move into it",
		"/Dev/", "/Dev/GH "+gh+"chriscz", "/Dev/Go/", "/Dev/Go/News "+lwn)
	e.B.Move(t, work+"/Dev/Go/", work, 0)
	latency(t, e.A, "folder moved up",
		"/Dev/", "/Dev/GH "+gh+"chriscz", "/Go/", "/Go/News "+lwn)
	settled(t, e, nil, "/Dev/", "/Dev/GH "+gh+"chriscz", "/Go/", "/Go/News "+lwn)
}

// TestDeleteRestoreAndPurge deletes a bookmark, restores it from _deleted,
// deletes it again and purges it, each in one browser.
func TestDeleteRestoreAndPurge(t *testing.T) {
	e := synced(t)
	e.A.Add(t, work, "News", lwn)
	both(t, e, "/GitHub "+gh, "/News "+lwn)

	e.B.Remove(t, work+"/GitHub "+gh)
	both(t, e, "/_deleted/", "/_deleted/GitHub "+gh, "/News "+lwn)

	// Restoring is moving it out of _deleted, in either profile.
	e.A.Move(t, work+"/_deleted/GitHub "+gh, work, -1)
	both(t, e, "/_deleted/", "/GitHub "+gh, "/News "+lwn)

	// Deleting a folder takes its contents to _deleted with it.
	e.A.Mkdir(t, work+"/Old")
	e.A.Move(t, work+"/News "+lwn, work+"/Old", -1)
	both(t, e, "/_deleted/", "/GitHub "+gh, "/Old/", "/Old/News "+lwn)
	e.B.Remove(t, work+"/Old/")
	both(t, e, "/_deleted/", "/_deleted/Old/", "/_deleted/Old/News "+lwn, "/GitHub "+gh)

	// Removing from _deleted is the one real deletion.
	e.A.Remove(t, work+"/_deleted/Old/")
	settled(t, e, nil, "/_deleted/", "/GitHub "+gh)
}

// TestConflictingEditsMakeSuffixedCopies gives one bookmark a different URL
// in each browser before either sees the other's. To make that certain the
// test holds the global lock, which every round takes before it reads or
// writes anything: both hosts wait, both edits land, and only then do the
// rounds run. Whichever runs first publishes its URL; the second sees the
// other one changed too and keeps both.
func TestConflictingEditsMakeSuffixedCopies(t *testing.T) {
	e := synced(t)
	unlock, err := db.Lock(e.Paths.GlobalLock())
	if err != nil {
		t.Fatal(err)
	}
	e.A.SetURL(t, work+"/GitHub "+gh, gh+"orgs/a")
	e.B.SetURL(t, work+"/GitHub "+gh, gh+"orgs/b")
	time.Sleep(1500 * time.Millisecond) // past the extension's 0.5 s quiet time
	unlock()
	// A round can start from a tree sent before its browser's edit, and
	// then finds its update stale; the round is retried and logs it.
	stale := regexp.MustCompile(`op failed .*err=stale|msg="the browser could not update .*stale`)
	settled(t, e, stale,
		fmt.Sprintf("/GitHub (%s) %sorgs/a", e.LabelA, gh),
		fmt.Sprintf("/GitHub (%s) %sorgs/b", e.LabelB, gh))
}

// TestFirstSyncUnionsExistingBookmarks starts with different bookmarks in
// each profile's folder: sharing it unions them and deletes nothing. The
// user is still editing in A when the config changes, so A's extension holds
// its tree back, and the host's view of A lacks the folder for a while.
func TestFirstSyncUnionsExistingBookmarks(t *testing.T) {
	e := setup(t)
	e.A.Mkdir(t, work+"/Dev")
	e.A.Add(t, work, "GitHub", gh)
	e.A.Add(t, work, "Mail", "https://fastmail.com/")
	e.A.Add(t, work+"/Dev", "Go", "https://go.dev/")
	e.B.Mkdir(t, work+"/Dev")
	e.B.Add(t, work, "GitHub", gh)
	e.B.Add(t, work, "Mail", "https://gmail.com/")
	e.B.Add(t, work, "News", lwn)
	e.B.Add(t, work+"/Dev", "Rust", "https://rust-lang.org/")
	e.Share(t)
	// Each edit restarts the extension's 0.5 s wait for quiet, and the host
	// looks at the config at least once a second.
	for i := range 8 {
		title := []string{"Go!", "Go"}[i%2]
		e.A.Rename(t, work+"/Dev/"+[]string{"Go", "Go!"}[i%2]+" https://go.dev/", title)
		time.Sleep(200 * time.Millisecond)
	}
	settled(t, e, nil,
		"/Dev/",
		"/Dev/Go https://go.dev/",
		"/Dev/Rust https://rust-lang.org/",
		"/GitHub "+gh,
		fmt.Sprintf("/Mail (%s) https://fastmail.com/", e.LabelA),
		fmt.Sprintf("/Mail (%s) https://gmail.com/", e.LabelB),
		"/News "+lwn)
}

// TestClosedBrowserCatchesUpOnStart edits in one browser while the other
// is closed, both ways round.
func TestClosedBrowserCatchesUpOnStart(t *testing.T) {
	e := synced(t)

	e.B.Close()
	e.A.Add(t, work, "News", lwn)
	e.A.Rename(t, work+"/GitHub "+gh, "GH")
	eventually(t, syncTimeout, "A to publish its edits", func() bool {
		return slices.Equal(recorded(t, e, e.DBA), []string{"/GH " + gh, "/News " + lwn})
	})
	before := time.Now()
	e.B = e.launch("b")
	// B's extension connects as the profile starts, with no page open, and
	// its host's first round applies A's edits and writes B's file.
	eventually(t, syncTimeout, "B to catch up", func() bool {
		return slices.Equal(recorded(t, e, e.DBB), []string{"/GH " + gh, "/News " + lwn})
	})
	caughtUp(t, e, e.DBB, before)
	e.B.open(t, e.ExtID)
	waitLines(t, e.B, work, []string{"/GH " + gh, "/News " + lwn}, syncTimeout)

	// The other way round, with a delete among the edits.
	e.A.Close()
	e.B.Remove(t, work+"/News "+lwn)
	e.B.Add(t, work, "Go", "https://go.dev/")
	eventually(t, syncTimeout, "B to publish its edits", func() bool {
		return len(recorded(t, e, e.DBB)) == 4
	})
	before = time.Now()
	e.A = e.launch("a")
	e.A.open(t, e.ExtID)
	settled(t, e, nil, "/GH "+gh, "/Go https://go.dev/", "/_deleted/", "/_deleted/News "+lwn)
	caughtUp(t, e, e.DBA, before)
}

// caughtUp checks that the profile's host wrote its file within the target
// of its hello. Its file changes only in the round that applies the other
// profile's edits, and the host touches the lock file on hello.
func caughtUp(t *testing.T, e *Env, id string, launched time.Time) {
	t.Helper()
	hello, err := os.Stat(e.Paths.ProfileLock(id))
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := os.Stat(e.Paths.ProfileFile(id))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("latency: %s connected %v after launch and caught up %v after connecting", id,
		hello.ModTime().Sub(launched).Round(time.Millisecond), wrote.ModTime().Sub(hello.ModTime()).Round(time.Millisecond))
	if !hello.ModTime().After(launched) {
		t.Errorf("%s did not connect after the launch", id)
	}
	if d := wrote.ModTime().Sub(hello.ModTime()); d > latencyTarget {
		t.Errorf("%s caught up %v after connecting; the target is %v", id, d, latencyTarget)
	}
}
