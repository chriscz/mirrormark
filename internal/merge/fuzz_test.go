package merge_test

import (
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
)

var fuzzSeeds = flag.Int("mirrormark.fuzz", 300, "seeds for TestFuzzUnion; -short runs 30")

// TestFuzzUnion runs three members through random edits, purges included,
// and rounds in which some members are closed and some batches fail part-way.
// After each burst of edits the union must reach a quiet sweep with every
// member holding the same bookmarks, and no URL may go missing unless a user
// purged it or changed it. Run more seeds with -mirrormark.fuzz=20000.
func TestFuzzUnion(t *testing.T) {
	seeds := *fuzzSeeds
	if testing.Short() {
		seeds = min(seeds, 30)
	}
	guarded, failed := 0, 0
	for seed := range seeds {
		err := fuzzSeed(uint64(seed))
		switch {
		case errors.Is(err, merge.ErrWrongTree):
			guarded++
		case err != nil:
			t.Errorf("seed %d: %v", seed, err)
			if failed++; failed > 10 {
				t.Fatal("too many failing seeds")
			}
		}
	}
	t.Logf("%d seeds, %d stopped by the wrong-tree guard", seeds, guarded)
}

type bnode struct{ id, title, url, path string }

func nodes(b *fakebrowser.Browser, id, path string, out *[]bnode) {
	for _, n := range b.Children(id) {
		p := path + "/" + n.Title
		*out = append(*out, bnode{n.ID, n.Title, n.URL, p})
		nodes(b, n.ID, p, out)
	}
}

func urls(m *member) map[string]bool {
	var all []bnode
	nodes(m.b, m.root, "", &all)
	out := map[string]bool{}
	for _, n := range all {
		if n.url != "" {
			out[n.url] = true
		}
	}
	return out
}

func inDeletedPath(path string) bool { return strings.HasPrefix(path, "/"+merge.DeletedTitle) }

func fuzzSeed(seed uint64) error {
	r := rand.New(rand.NewPCG(seed, 7))
	s := newSim("p", "q", "r")
	failing := false
	s.fail = func(int) bool { return failing && r.IntN(4) == 0 }
	u := 0
	gone := map[string]bool{} // URLs a user purged or changed
	for epoch := range 6 {
		for _, m := range s.ms {
			for range r.IntN(4) {
				edit(r, m, epoch, &u, gone)
				m.edited = true
			}
		}
		want := map[string]bool{}
		for _, m := range s.ms {
			for k := range urls(m) {
				want[k] = true
			}
		}
		// Three sweeps with closed members and failing batches, then
		// sweeps until one is quiet.
		failing = epoch%2 == 0
		quiet := false
		for sweep := 0; sweep < 15 && !quiet; sweep++ {
			if sweep == 3 {
				failing = false
			}
			quiet = sweep >= 3
			for _, m := range s.ms {
				if sweep < 3 && r.IntN(5) == 0 {
					continue // closed this sweep
				}
				w, err := s.round(m)
				if err != nil {
					return fmt.Errorf("epoch %d: %w", epoch, err)
				}
				quiet = quiet && !w
			}
		}
		if !quiet {
			return fmt.Errorf("epoch %d: no quiet sweep", epoch)
		}
		if err := s.same(); err != nil {
			return fmt.Errorf("epoch %d: %w", epoch, err)
		}
		for _, m := range s.ms {
			got := urls(m)
			for k := range want {
				if !got[k] && !gone[k] {
					return fmt.Errorf("epoch %d: %s lost %s", epoch, m.label, k)
				}
			}
		}
	}
	return nil
}

// edit makes one random user edit in m's member folder and records in gone
// every URL that may rightly vanish because of it.
func edit(r *rand.Rand, m *member, epoch int, u *int, gone map[string]bool) {
	var all, folders []bnode
	nodes(m.b, m.root, "", &all)
	for _, n := range all {
		if n.url == "" {
			folders = append(folders, n)
		}
	}
	folder := func() string {
		if len(folders) == 0 || r.IntN(3) == 0 {
			return m.root
		}
		return folders[r.IntN(len(folders))].id
	}
	k := r.IntN(10)
	if len(all) == 0 {
		k = 0
	}
	switch {
	case k < 3: // add; titles repeat so adds collide
		*u++
		if r.IntN(4) == 0 {
			m.b.Add(folder(), []string{"F", "G", merge.DeletedTitle}[r.IntN(3)], "")
		} else {
			m.b.Add(folder(), []string{"A", "B", "C"}[r.IntN(3)], fmt.Sprintf("https://%d/", *u%7+epoch))
		}
	case k < 5:
		n := all[r.IntN(len(all))]
		m.b.Rename(n.id, n.title+"x")
	case k < 6:
		if n := all[r.IntN(len(all))]; n.url != "" {
			*u++
			gone[n.url] = true
			m.b.SetURL(n.id, fmt.Sprintf("https://%d/", *u))
		}
	case k < 8:
		n, p := all[r.IntN(len(all))], folder()
		for _, x := range all {
			if x.id == p && (x.path == n.path || strings.HasPrefix(x.path, n.path+"/")) {
				return // into itself
			}
		}
		m.b.Move(n.id, p)
	default: // remove: a purge if it is in _deleted here or in m's record
		n := all[r.IntN(len(all))]
		sub := []bnode{n}
		nodes(m.b, n.id, n.path, &sub)
		var recorded []string
		for _, l := range fakebrowser.NodeLines(m.rec) {
			if inDeletedPath(l) {
				recorded = append(recorded, l)
			}
		}
		for _, x := range sub {
			purged := inDeletedPath(n.path)
			for _, l := range recorded {
				purged = purged || x.url != "" && strings.HasSuffix(l, " "+x.url)
			}
			if purged {
				for _, y := range sub {
					gone[y.url] = true
				}
			}
		}
		m.b.RemoveTree(n.id)
	}
}
