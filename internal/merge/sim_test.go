package merge_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
)

// member is one profile in a simulated union: its browser, with the member
// folder at root, and the record it last published.
type member struct {
	label string
	b     *fakebrowser.Browser
	root  string
	rec   []*db.Node
	has   bool
	seq   int
}

// sim is a union of members whose rounds run one at a time, as under the
// global lock. fail, if not nil, injects op failures into every batch.
type sim struct {
	ms   []*member
	n    int
	fail func(i int) bool
}

func newSim(labels ...string) *sim {
	s := &sim{}
	for _, l := range labels {
		b := fakebrowser.New()
		s.ms = append(s.ms, &member{label: l, b: b, root: b.Add("1", "U", "")})
	}
	return s
}

// fill gives created nodes their browser ids, as the host does from the
// batch's results before it writes the file.
func fill(ns []*db.Node, ids map[string]string) {
	for _, n := range ns {
		if n.ID == "" {
			n.ID = ids[n.M]
		}
		fill(n.Children, ids)
	}
}

// round runs one round for m as the host does: merge against the newest
// record, apply the ops, and publish only if every op landed and the tree
// changed. It reports whether m published.
func (s *sim) round(m *member) (bool, error) {
	var theirs []*db.Node
	best := -1
	for _, o := range s.ms {
		if o.has && o.seq > best {
			best, theirs = o.seq, o.rec
		}
	}
	res, err := merge.Round(merge.Input{Base: m.rec, HasBase: m.has, Ours: m.b.Children(m.root), Theirs: theirs,
		RootID: m.root, Label: m.label, NewM: func() string { s.n++; return fmt.Sprintf("m-s%d", s.n) }})
	if err != nil {
		return false, fmt.Errorf("%s: %w", m.label, err)
	}
	ids := map[string]string{}
	for i, r := range m.b.Apply(res.Ops, s.fail) {
		if r.Error == "injected" {
			return false, nil // the batch stopped here; no file is written
		}
		if !r.OK {
			return false, fmt.Errorf("%s: op %+v: %s", m.label, res.Ops[i], r.Error)
		}
		if res.Ops[i].Op == "create" {
			ids[res.Ops[i].Ref] = r.ID
		}
	}
	if got, want := m.b.Lines(m.root), fakebrowser.NodeLines(res.Tree); !slices.Equal(got, want) {
		return false, fmt.Errorf("%s: browser after ops:\n%q\nmerged tree:\n%q", m.label, got, want)
	}
	fill(res.Tree, ids)
	if m.has && db.EqualNodes(res.Tree, m.rec) {
		return false, nil
	}
	m.rec, m.has, m.seq = res.Tree, true, best+1
	return true, nil
}

// settle runs sweeps of rounds over every member until one sweep publishes
// nothing, then checks that every member holds the same union.
func (s *sim) settle() error {
	for range 12 {
		wrote := false
		for _, m := range s.ms {
			w, err := s.round(m)
			if err != nil {
				return err
			}
			wrote = wrote || w
		}
		if !wrote {
			return s.same()
		}
	}
	return fmt.Errorf("no quiet sweep after 12")
}

func (s *sim) same() error {
	a := s.ms[0].b.Lines(s.ms[0].root)
	for _, m := range s.ms[1:] {
		if b := m.b.Lines(m.root); !slices.Equal(a, b) {
			return fmt.Errorf("quiet but diverged:\n%s %q\n%s %q", s.ms[0].label, a, m.label, b)
		}
	}
	return nil
}

func (s *sim) want(t *testing.T, want ...string) {
	t.Helper()
	if err := s.settle(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if got := s.ms[0].b.Lines(s.ms[0].root); !slices.Equal(got, want) {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

// Three members adding one title with three URLs all end up suffixed, not
// two suffixed and the third plain.
func TestThreeSameTitleAddsAllGetSuffixes(t *testing.T) {
	s := newSim("p", "q", "r")
	for i, m := range s.ms {
		m.b.Add(m.root, "A", fmt.Sprintf("https://a/%d", i))
	}
	s.want(t, "/A (p) https://a/0", "/A (q) https://a/1", "/A (r) https://a/2")
}

func TestSimConflictsDeletesAndPurges(t *testing.T) {
	s := newSim("p", "q")
	p, q := s.ms[0], s.ms[1]
	p.b.Add(p.root, "GH", "https://gh/")
	p.b.Add(p.b.Add(p.root, "Dev", ""), "Go", "https://go/")
	p.b.Add(p.root, "X", "")
	p.b.Add(p.root, "Y", "")
	s.want(t, "/Dev/", "/Dev/Go https://go/", "/GH https://gh/", "/X/", "/Y/")

	p.b.SetURL(p.b.Find(p.root, "GH"), "https://a/")
	q.b.SetURL(q.b.Find(q.root, "GH"), "https://b/")
	p.b.RemoveTree(p.b.Find(p.root, "Dev"))
	q.b.Add(q.b.Find(q.root, "Dev"), "Rust", "https://rust/")
	p.b.Move(p.b.Find(p.root, "X"), p.b.Find(p.root, "Y"))
	q.b.Move(q.b.Find(q.root, "Y"), q.b.Find(q.root, "X"))
	q.b.Add(q.root, "Mail", "https://gmail/")
	p.b.Add(p.root, "Mail", "https://fastmail/")
	s.want(t, "/GH (p) https://a/", "/GH (q) https://b/", "/Mail (p) https://fastmail/", "/Mail (q) https://gmail/",
		"/X/", "/X/Y/", "/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Go https://go/", "/_deleted/Dev/Rust https://rust/")

	// A second conflict on a copy replaces its suffix.
	p.b.SetURL(p.b.Find(p.root, "GH (q)"), "https://c/")
	q.b.SetURL(q.b.Find(q.root, "GH (q)"), "https://d/")
	s.want(t, "/GH (p) https://a/", "/GH (p) https://c/", "/GH (q) https://d/", "/Mail (p) https://fastmail/",
		"/Mail (q) https://gmail/", "/X/", "/X/Y/", "/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Go https://go/",
		"/_deleted/Dev/Rust https://rust/")

	q.b.RemoveTree(q.b.Find(q.root, "_deleted"))
	s.want(t, "/GH (p) https://a/", "/GH (p) https://c/", "/GH (q) https://d/", "/Mail (p) https://fastmail/",
		"/Mail (q) https://gmail/", "/X/", "/X/Y/")
}
