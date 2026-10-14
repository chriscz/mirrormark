package fakebrowser

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
)

var fuzzSeeds = flag.Int("mirrormark.fuzz", 300, "seeds for the union fuzz tests; -short runs 30")

// Seeds is how many seeds a union fuzz test runs: 300, 30 with -short, or
// what -mirrormark.fuzz says.
func Seeds() int {
	if testing.Short() {
		return min(*fuzzSeeds, 30)
	}
	return *fuzzSeeds
}

// Union is a simulated union of members whose rounds run one at a time, as
// under the global lock. Fuzz drives it.
type Union interface {
	Len() int
	// Member returns member i's browser, the id of its member folder and
	// the union record it last published.
	Member(i int) (b *Browser, root string, rec []*db.Node)
	// Edited tells member i that its user just edited the member folder.
	Edited(i int)
	// Round runs one round for member i, failing the ops fail picks. It
	// reports whether the round changed anything. A batch stopped by an
	// injected failure returns false and no error.
	Round(i int, fail func(int) bool) (bool, error)
}

// Fuzz runs the members of u through random edits, purges included, and
// rounds in which some members are closed and some batches fail part-way.
// After each burst of edits the union must reach a quiet sweep with every
// member holding the same bookmarks, and no URL may go missing unless a user
// purged it or changed it. budget, if not negative, caps the nodes users
// remove in each burst, so the wrong-tree guard cannot trip.
func Fuzz(seed uint64, u Union, budget int) error {
	r := rand.New(rand.NewPCG(seed, 7))
	failing := false
	fail := func(int) bool { return failing && r.IntN(4) == 0 }
	n := 0
	gone := map[string]bool{} // URLs a user purged or changed
	for epoch := range 6 {
		left := budget
		for i := range u.Len() {
			for range r.IntN(4) {
				b, root, rec := u.Member(i)
				edit(r, b, root, rec, epoch, &n, gone, &left)
				u.Edited(i)
			}
		}
		want := map[string]bool{}
		for i := range u.Len() {
			for k := range urls(u, i) {
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
			for i := range u.Len() {
				if sweep < 3 && r.IntN(5) == 0 {
					continue // closed this sweep
				}
				w, err := u.Round(i, fail)
				if err != nil {
					return fmt.Errorf("epoch %d: %w", epoch, err)
				}
				quiet = quiet && !w
			}
		}
		if !quiet {
			return fmt.Errorf("epoch %d: no quiet sweep", epoch)
		}
		if err := Same(u); err != nil {
			return fmt.Errorf("epoch %d: %w", epoch, err)
		}
		for i := range u.Len() {
			got := urls(u, i)
			for k := range want {
				if !got[k] && !gone[k] {
					return fmt.Errorf("epoch %d: member %d lost %s", epoch, i, k)
				}
			}
		}
	}
	return nil
}

// Same checks that every member of u holds the same bookmarks.
func Same(u Union) error {
	b0, root0, _ := u.Member(0)
	a := b0.Lines(root0)
	for i := 1; i < u.Len(); i++ {
		b, root, _ := u.Member(i)
		if l := b.Lines(root); !slices.Equal(a, l) {
			return fmt.Errorf("quiet but diverged:\nmember 0 %q\nmember %d %q", a, i, l)
		}
	}
	return nil
}

type fnode struct{ id, title, url, path string }

func walk(b *Browser, id, path string, out *[]fnode) {
	for _, n := range b.Children(id) {
		p := path + "/" + n.Title
		*out = append(*out, fnode{n.ID, n.Title, n.URL, p})
		walk(b, n.ID, p, out)
	}
}

func urls(u Union, i int) map[string]bool {
	b, root, _ := u.Member(i)
	var all []fnode
	walk(b, root, "", &all)
	out := map[string]bool{}
	for _, n := range all {
		if n.url != "" {
			out[n.url] = true
		}
	}
	return out
}

func inDeletedPath(path string) bool { return strings.HasPrefix(path, "/"+merge.DeletedTitle) }

// edit makes one random user edit in the member folder root of b and
// records in gone every URL that may rightly vanish because of it. rec is
// the member's last record. A remove of more nodes than *left is skipped.
func edit(r *rand.Rand, b *Browser, root string, rec []*db.Node, epoch int, u *int, gone map[string]bool, left *int) {
	var all, folders []fnode
	walk(b, root, "", &all)
	for _, n := range all {
		if n.url == "" {
			folders = append(folders, n)
		}
	}
	folder := func() string {
		if len(folders) == 0 || r.IntN(3) == 0 {
			return root
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
			b.Add(folder(), []string{"F", "G", merge.DeletedTitle}[r.IntN(3)], "")
		} else {
			b.Add(folder(), []string{"A", "B", "C"}[r.IntN(3)], fmt.Sprintf("https://%d/", *u%7+epoch))
		}
	case k < 5:
		n := all[r.IntN(len(all))]
		b.Rename(n.id, n.title+"x")
	case k < 6:
		if n := all[r.IntN(len(all))]; n.url != "" {
			*u++
			gone[n.url] = true
			b.SetURL(n.id, fmt.Sprintf("https://%d/", *u))
		}
	case k < 8:
		n, p := all[r.IntN(len(all))], folder()
		for _, x := range all {
			if x.id == p && (x.path == n.path || strings.HasPrefix(x.path, n.path+"/")) {
				return // into itself
			}
		}
		b.Move(n.id, p)
	default: // remove: a purge if it is in _deleted here or in the record
		n := all[r.IntN(len(all))]
		sub := []fnode{n}
		walk(b, n.id, n.path, &sub)
		if *left >= 0 {
			if len(sub) > *left {
				return
			}
			*left -= len(sub)
		}
		var recorded []string
		for _, l := range NodeLines(rec) {
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
		b.RemoveTree(n.id)
	}
}
