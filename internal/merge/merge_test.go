package merge_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
)

const (
	me    = "chrome/personal"
	other = "chrome/work"
)

// fixture is profile P's browser with member folder "Work" on the bar.
type fixture struct {
	t    *testing.T
	b    *fakebrowser.Browser
	root string
	base []*db.Node
}

// newFixture builds P's browser with fill, then records it as base: every
// node gets m "m-<title>" and by "chrome/work".
func newFixture(t *testing.T, fill func(b *fakebrowser.Browser, root string)) *fixture {
	b := fakebrowser.New()
	root := b.Add("1", "Work", "")
	fill(b, root)
	base := b.Children(root)
	var mark func(ns []*db.Node)
	mark = func(ns []*db.Node) {
		for _, n := range ns {
			n.M, n.By = "m-"+n.Title, other
			mark(n.Children)
		}
	}
	mark(base)
	return &fixture{t: t, b: b, root: root, base: base}
}

// theirs returns a copy of base, changed by edit, without browser ids.
func (f *fixture) theirs(edit func(ns []*db.Node) []*db.Node) []*db.Node {
	var cp func(ns []*db.Node) []*db.Node
	cp = func(ns []*db.Node) []*db.Node {
		var out []*db.Node
		for _, n := range ns {
			c := *n
			c.ID = ""
			c.Children = cp(n.Children)
			out = append(out, &c)
		}
		return out
	}
	return edit(cp(f.base))
}

func find(ns []*db.Node, title string) *db.Node {
	for _, n := range ns {
		if n.Title == title {
			return n
		}
		if c := find(n.Children, title); c != nil {
			return c
		}
	}
	return nil
}

func freshMs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("m-new%d", n) }
}

// round merges, applies the ops to P's browser, and checks that the browser
// now matches the merged tree.
func (f *fixture) round(theirs []*db.Node) merge.Result {
	f.t.Helper()
	res, err := merge.Round(merge.Input{Base: f.base, HasBase: f.base != nil, Ours: f.b.Children(f.root),
		Theirs: theirs, RootID: f.root, Label: me, NewM: freshMs()})
	if err != nil {
		f.t.Fatalf("Round: %v", err)
	}
	for i, r := range f.b.Apply(res.Ops, nil) {
		if !r.OK {
			f.t.Fatalf("op %d %+v failed: %s", i, res.Ops[i], r.Error)
		}
	}
	if got, want := f.b.Lines(f.root), fakebrowser.NodeLines(res.Tree); !slices.Equal(got, want) {
		f.t.Fatalf("browser after ops:\n%q\nmerged tree:\n%q", got, want)
	}
	return res
}

func (f *fixture) want(want ...string) {
	f.t.Helper()
	slices.Sort(want)
	if got := f.b.Lines(f.root); !slices.Equal(got, want) {
		f.t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func gh(b *fakebrowser.Browser, root string) { b.Add(root, "GitHub", "https://github.com/") }

func TestQuietRoundChangesNothing(t *testing.T) {
	f := newFixture(t, gh)
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	if len(res.Ops) != 0 {
		t.Fatalf("ops %+v, want none", res.Ops)
	}
	if !db.EqualNodes(res.Tree, f.base) {
		t.Fatalf("tree %+v differs from base", res.Tree)
	}
}

func TestTheirChangeIsApplied(t *testing.T) {
	f := newFixture(t, gh)
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GitHub https://github.com/orgs/b")
}

func TestOurChangeIsKeptAndSigned(t *testing.T) {
	f := newFixture(t, gh)
	f.b.Rename(f.b.Find(f.root, "GitHub"), "GH")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	if len(res.Ops) != 0 || res.Tree[0].By != me || res.Tree[0].M != "m-GitHub" {
		t.Fatalf("ops %+v tree %+v", res.Ops, res.Tree[0])
	}
}

func TestSameChangeOnBothSides(t *testing.T) {
	f := newFixture(t, gh)
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/x")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/x"; return ns }))
	if len(res.Ops) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("ops %+v conflicts %+v", res.Ops, res.Conflicts)
	}
}

func TestURLConflictMakesSuffixedCopies(t *testing.T) {
	f := newFixture(t, gh)
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/orgs/a")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GitHub (chrome/personal) https://github.com/orgs/a", "/GitHub (chrome/work) https://github.com/orgs/b")
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if n := find(res.Tree, "GitHub (chrome/work)"); n.M != "m-GitHub" || n.Suffix != other {
		t.Fatalf("theirs copy %+v should keep m and carry the suffix", n)
	}
	if n := find(res.Tree, "GitHub (chrome/personal)"); n.M == "m-GitHub" || n.ID == "" || n.Suffix != me {
		t.Fatalf("our copy %+v should be the browser's node with a new m", n)
	}
}

func TestTitleAndURLEditsCombine(t *testing.T) {
	f := newFixture(t, gh)
	f.b.Rename(f.b.Find(f.root, "GitHub"), "GH")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GH https://github.com/orgs/b")
}

func news(b *fakebrowser.Browser, root string) { b.Add(root, "News", "https://lwn.net/") }

func TestDeleteMovesToDeleted(t *testing.T) {
	f := newFixture(t, news)
	f.b.RemoveTree(f.b.Find(f.root, "News"))
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/_deleted/", "/_deleted/News https://lwn.net/")
	if n := find(res.Tree, "News"); n.M != "m-News" {
		t.Fatalf("deleted node lost its m: %+v", n)
	}
}

func TestDeleteKeepsTheirRename(t *testing.T) {
	f := newFixture(t, news)
	f.b.RemoveTree(f.b.Find(f.root, "News"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].Title = "LWN"; return ns }))
	f.want("/_deleted/", "/_deleted/LWN https://lwn.net/")
}

func TestDeletedFolderKeepsItsSubtree(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		d := b.Add(root, "Dev", "")
		b.Add(d, "Go", "https://go.dev/")
		b.Add(root, "A", "https://a/")
		b.Add(root, "B", "https://b/")
	})
	f.b.RemoveTree(f.b.Find(f.root, "Dev"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/A https://a/", "/B https://b/", "/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Go https://go.dev/")
}

func TestSameTitleAddsConflict(t *testing.T) {
	f := newFixture(t, func(*fakebrowser.Browser, string) {})
	f.b.Add(f.root, "Mail", "https://fastmail.com/")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return append(ns, &db.Node{Title: "Mail", URL: "https://gmail.com/", M: "m-t1", By: other})
	}))
	f.want("/Mail (chrome/personal) https://fastmail.com/", "/Mail (chrome/work) https://gmail.com/")
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestIdenticalAddsLink(t *testing.T) {
	f := newFixture(t, func(*fakebrowser.Browser, string) {})
	d := f.b.Add(f.root, "Dev", "")
	f.b.Add(d, "Go", "https://go.dev/")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return append(ns, &db.Node{Title: "Dev", M: "m-t1", By: other, Children: []*db.Node{
			{Title: "Go", URL: "https://go.dev/", M: "m-t2", By: other},
			{Title: "Rust", URL: "https://rust-lang.org/", M: "m-t3", By: other},
		}})
	}))
	f.want("/Dev/", "/Dev/Go https://go.dev/", "/Dev/Rust https://rust-lang.org/")
	if find(res.Tree, "Dev").M != "m-t1" || find(res.Tree, "Go").M != "m-t2" {
		t.Fatalf("adds did not link: %+v", res.Tree)
	}
}

func TestFirstRoundUnionsAndDeletesNothing(t *testing.T) {
	b := fakebrowser.New()
	root := b.Add("1", "Work", "")
	b.Add(root, "GitHub", "https://github.com/")
	b.Add(root, "Mail", "https://fastmail.com/")
	theirs := []*db.Node{
		{Title: "GitHub", URL: "https://github.com/", M: "m-1", By: other},
		{Title: "Mail", URL: "https://gmail.com/", M: "m-2", By: other},
		{Title: "News", URL: "https://lwn.net/", M: "m-3", By: other},
	}
	f := &fixture{t: t, b: b, root: root}
	f.round(theirs)
	f.want("/GitHub https://github.com/", "/Mail (chrome/personal) https://fastmail.com/",
		"/Mail (chrome/work) https://gmail.com/", "/News https://lwn.net/")
}

func inDeleted(b *fakebrowser.Browser, root string) {
	d := b.Add(root, "_deleted", "")
	b.Add(d, "Old", "https://old/")
	b.Add(root, "Keep", "https://keep/")
}

func TestOurPurgeRemovesIt(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.RemoveTree(f.b.Find(f.b.Find(f.root, "_deleted"), "Old"))
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/Keep https://keep/", "/_deleted/")
	if find(res.Tree, "Old") != nil {
		t.Fatal("purged node is still in the tree")
	}
}

func TestTheirPurgeRemovesItHere(t *testing.T) {
	f := newFixture(t, inDeleted)
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		find(ns, "_deleted").Children = nil
		return ns
	}))
	f.want("/Keep https://keep/", "/_deleted/")
	if len(res.Ops) != 1 || res.Ops[0].Op != "remove" {
		t.Fatalf("ops %+v, want one remove", res.Ops)
	}
}

func TestEditBeatsPurge(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.RemoveTree(f.b.Find(f.b.Find(f.root, "_deleted"), "Old"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { find(ns, "Old").Title = "Older"; return ns }))
	f.want("/Keep https://keep/", "/_deleted/", "/_deleted/Older https://old/")
}

func TestRemovingDeletedFolderPurgesItsContents(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.RemoveTree(f.b.Find(f.root, "_deleted"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/Keep https://keep/")
}

func TestSecondConflictReplacesSuffix(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "GitHub (chrome/work)", "https://x/") })
	f.base[0].Suffix = other
	f.b.SetURL(f.b.Find(f.root, "GitHub (chrome/work)"), "https://a/")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL, ns[0].By = "https://b/", "chrome/third"; return ns }))
	f.want("/GitHub (chrome/personal) https://a/", "/GitHub (chrome/third) https://b/")
}

func TestTypedParenthesesAreNotASuffix(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "Notes (work)", "https://x/") })
	f.b.SetURL(f.b.Find(f.root, "Notes (work)"), "https://a/")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://b/"; return ns }))
	f.want("/Notes (work) (chrome/personal) https://a/", "/Notes (work) (chrome/work) https://b/")
}

func TestFolderConflictTheirsWins(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(b.Add(root, "Dev", ""), "Go", "https://go.dev/") })
	f.b.Rename(f.b.Find(f.root, "Dev"), "Code")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].Title = "Programming"; return ns }))
	f.want("/Programming/", "/Programming/Go https://go.dev/")
	if len(res.Conflicts) != 1 || !res.Conflicts[0].Folder {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestCycleUndoesTheirMove(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "X", ""); b.Add(root, "Y", "") })
	f.b.Move(f.b.Find(f.root, "X"), f.b.Find(f.root, "Y")) // ours: X inside Y
	f.round(f.theirs(func(ns []*db.Node) []*db.Node {      // theirs: Y inside X
		x, y := ns[0], ns[1]
		x.Children = []*db.Node{y}
		return []*db.Node{x}
	}))
	f.want("/Y/", "/Y/X/")
}

func TestWrongTreeGuard(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		for i := range 8 {
			b.Add(root, fmt.Sprintf("B%d", i), fmt.Sprintf("https://b/%d", i))
		}
	})
	for i := range 5 {
		f.b.RemoveTree(f.b.Find(f.root, fmt.Sprintf("B%d", i)))
	}
	_, err := merge.Round(merge.Input{Base: f.base, HasBase: true, Ours: f.b.Children(f.root),
		Theirs: f.base, RootID: f.root, Label: me, NewM: freshMs()})
	if !errors.Is(err, merge.ErrWrongTree) {
		t.Fatalf("err %v, want ErrWrongTree", err)
	}
}

func TestRetryAfterPartialFailureDoesNotDuplicate(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { gh(b, root); news(b, root) })
	f.b.RemoveTree(f.b.Find(f.root, "News"))
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/orgs/a")
	theirs := f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns })
	res, err := merge.Round(merge.Input{Base: f.base, HasBase: true, Ours: f.b.Children(f.root),
		Theirs: theirs, RootID: f.root, Label: me, NewM: freshMs()})
	if err != nil {
		t.Fatal(err)
	}
	// Every create lands, then the first other op fails and the batch stops,
	// as if the browser stopped answering. Base stays as it was: nothing was
	// written.
	f.b.Apply(res.Ops, func(i int) bool { return res.Ops[i].Op != "create" })
	f.round(theirs)
	f.want("/GitHub (chrome/personal) https://github.com/orgs/a", "/GitHub (chrome/work) https://github.com/orgs/b",
		"/_deleted/", "/_deleted/News https://lwn.net/")
}

func TestNewBookmarkTakesItsSourcePosition(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		b.Add(root, "A", "https://a/")
		b.Add(root, "C", "https://c/")
	})
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return slices.Insert(ns, 1, &db.Node{Title: "B", URL: "https://b/", M: "m-B", By: other})
	}))
	var titles []string
	for _, n := range res.Tree {
		titles = append(titles, n.Title)
	}
	if !slices.Equal(titles, []string{"A", "B", "C"}) {
		t.Fatalf("order %q", titles)
	}
}

func TestDeletedFolderTakesTheirNewBookmark(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(b.Add(root, "Dev", ""), "Go", "https://go.dev/") })
	f.b.RemoveTree(f.b.Find(f.root, "Dev"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		ns[0].Children = append(ns[0].Children, &db.Node{Title: "Rust", URL: "https://rust-lang.org/", M: "m-t1", By: other})
		return ns
	}))
	f.want("/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Go https://go.dev/", "/_deleted/Dev/Rust https://rust-lang.org/")
}

func TestOurEditBeatsTheirPurge(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.Rename(f.b.Find(f.b.Find(f.root, "_deleted"), "Old"), "Older")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { find(ns, "_deleted").Children = nil; return ns }))
	f.want("/Keep https://keep/", "/_deleted/", "/_deleted/Older https://old/")
}

func TestPurgedFolderStaysForAnEditInside(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		b.Add(b.Add(b.Add(root, "_deleted", ""), "Dev", ""), "Go", "https://go.dev/")
	})
	f.b.Rename(f.b.Find(f.b.Find(f.b.Find(f.root, "_deleted"), "Dev"), "Go"), "Golang")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { find(ns, "_deleted").Children = nil; return ns }))
	f.want("/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Golang https://go.dev/")
}

func TestAddsOnBothSidesUnion(t *testing.T) {
	f := newFixture(t, gh)
	f.b.Add(f.root, "Mail", "https://fastmail.com/")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return append(ns, &db.Node{Title: "News", URL: "https://lwn.net/", M: "m-t1", By: other})
	}))
	f.want("/GitHub https://github.com/", "/Mail https://fastmail.com/", "/News https://lwn.net/")
	if n := find(res.Tree, "Mail"); n.M == "" || n.By != me {
		t.Fatalf("our add %+v needs a fresh m and our label", n)
	}
}

func TestTheirMoveIsApplied(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "Dev", ""); gh(b, root) })
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		ns[0].Children = []*db.Node{ns[1]}
		return ns[:1]
	}))
	f.want("/Dev/", "/Dev/GitHub https://github.com/")
	if len(res.Ops) != 1 || res.Ops[0].Op != "move" || res.Ops[0].Expect.Parent != f.root {
		t.Fatalf("ops %+v, want one move from the member folder", res.Ops)
	}
}

func TestLocalOrderIsKept(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		for _, s := range []string{"A", "B", "C"} {
			b.Add(root, s, "https://"+s+"/")
		}
	})
	f.b.Move(f.b.Find(f.root, "A"), f.root) // A goes last
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	var titles []string
	for _, n := range res.Tree {
		titles = append(titles, n.Title)
	}
	if len(res.Ops) != 0 || !slices.Equal(titles, []string{"B", "C", "A"}) {
		t.Fatalf("ops %+v order %q", res.Ops, titles)
	}
}

// Once the user retitles a node so it no longer ends in the suffix, the
// whole title is theirs and a later conflict leaves it intact.
func TestSuffixDropsWhenRetitled(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "GitHub (chrome/work)", "https://x/") })
	f.base[0].Suffix = other
	id := f.b.Find(f.root, "GitHub (chrome/work)")
	f.b.Rename(id, "GitHub (chrome/work) old")
	f.b.SetURL(id, "https://a/")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://b/"; return ns }))
	f.want("/GitHub (chrome/work) old (chrome/personal) https://a/", "/GitHub (chrome/work) https://b/")
}

func TestWrongTreeGuardThreshold(t *testing.T) {
	for _, c := range []struct{ nodes, removed int }{{8, 4}, {5, 5}} {
		f := newFixture(t, func(b *fakebrowser.Browser, root string) {
			for i := range c.nodes {
				b.Add(root, fmt.Sprintf("B%d", i), fmt.Sprintf("https://b/%d", i))
			}
		})
		for i := range c.removed {
			f.b.RemoveTree(f.b.Find(f.root, fmt.Sprintf("B%d", i)))
		}
		f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	}
}

func TestOurPurgeKeepsAFolderTheyAddedTo(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		b.Add(b.Add(b.Add(root, "_deleted", ""), "Dev", ""), "Go", "https://go.dev/")
	})
	f.b.RemoveTree(f.b.Find(f.b.Find(f.root, "_deleted"), "Dev"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		d := find(ns, "Dev")
		d.Children = append(d.Children, &db.Node{Title: "Rust", URL: "https://rust-lang.org/", M: "m-t1", By: other})
		return ns
	}))
	f.want("/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Rust https://rust-lang.org/")
}

// Undoing their move of Y can put Y back under A, which their purge dropped:
// A must come back too, or X and Y hang from nothing.
func TestCycleUndoKeepsAParentTheyPurged(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		a := b.Add(b.Add(root, "_deleted", ""), "A", "")
		b.Add(b.Add(a, "Y", ""), "y1", "https://y1/")
		b.Add(b.Add(root, "X", ""), "x1", "https://x1/")
	})
	y := f.b.Find(f.b.Find(f.b.Find(f.root, "_deleted"), "A"), "Y")
	f.b.Move(f.b.Find(f.root, "X"), y)                // ours: X into Y
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { // theirs: Y into X, then purge A
		d, x := ns[0], ns[1]
		x.Children = append(x.Children, d.Children[0].Children[0])
		d.Children = nil
		return ns
	}))
	f.want("/_deleted/", "/_deleted/A/", "/_deleted/A/Y/", "/_deleted/A/Y/y1 https://y1/",
		"/_deleted/A/Y/X/", "/_deleted/A/Y/X/x1 https://x1/")
}

// Their purge of F comes with a move of K out of F. If the move fails, the
// remove of F must not take K with it, and the retry finishes the job.
func TestFailedMoveOutOfARemovedFolderKeepsTheNode(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		b.Add(b.Add(b.Add(root, "_deleted", ""), "F", ""), "K", "https://k/")
	})
	theirs := f.theirs(func(ns []*db.Node) []*db.Node {
		d := ns[0]
		k := d.Children[0].Children[0]
		d.Children = nil
		return append(ns, k)
	})
	res, err := merge.Round(merge.Input{Base: f.base, HasBase: true, Ours: f.b.Children(f.root),
		Theirs: theirs, RootID: f.root, Label: me, NewM: freshMs()})
	if err != nil {
		t.Fatal(err)
	}
	f.b.Apply(res.Ops, func(i int) bool { return res.Ops[i].Op == "move" })
	f.want("/_deleted/", "/_deleted/F/", "/_deleted/F/K https://k/")
	f.round(theirs)
	f.want("/K https://k/", "/_deleted/")
}

// Removes are non-recursive and leaf-first, as with bookmarks.remove: a
// folder goes only once it is empty.
func TestRemovesTakeContentsBeforeTheirFolder(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		g := b.Add(b.Add(b.Add(root, "_deleted", ""), "F", ""), "G", "")
		b.Add(g, "a", "https://a/")
		b.Add(g, "b", "https://b/")
	})
	d := f.b.Find(f.root, "_deleted")
	if r := f.b.Apply([]merge.Op{{Op: "remove", ID: f.b.Find(d, "F"), Expect: &merge.Expect{Title: new("F"), URL: new("")}}}, nil); r[0].OK {
		t.Fatal("removed a folder that is not empty")
	}
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].Children = nil; return ns }))
	var got []string
	for _, op := range res.Ops {
		got = append(got, op.Op+" "+*op.Expect.Title)
	}
	if want := []string{"remove a", "remove b", "remove G", "remove F"}; !slices.Equal(got, want) {
		t.Fatalf("ops %q, want %q", got, want)
	}
	f.want("/_deleted/")
}

// After a failed op the rest of the batch does not run, as in the extension.
func TestApplyStopsAtTheFirstFailure(t *testing.T) {
	b := fakebrowser.New()
	ops := []merge.Op{
		{Op: "create", Ref: "r1", Parent: "1", Title: new("A"), URL: new("https://a/")},
		{Op: "update", ID: "99", Title: new("x"), Expect: &merge.Expect{Title: new("x"), URL: new("")}},
		{Op: "create", Ref: "r2", Parent: "1", Title: new("B"), URL: new("https://b/")},
	}
	res := b.Apply(ops, nil)
	if !res[0].OK || res[1].OK || res[2].OK || res[2].Error != "not run" {
		t.Fatalf("results %+v", res)
	}
	if got := b.Lines("1"); !slices.Equal(got, []string{"/A https://a/"}) {
		t.Fatalf("browser %q", got)
	}
}
