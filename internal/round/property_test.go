package round_test

import (
	"fmt"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
	"go.yaml.in/yaml/v3"
)

type member struct {
	b    *fakebrowser.Browser
	root string
	file *db.DB
}

// hosts is a union at toolbar/U whose rounds go through round.Make and
// Finish as the host's would, each database file through YAML as on disk.
type hosts struct {
	cfg *db.Config
	ms  []*member
}

func newHosts() *hosts {
	h := &hosts{cfg: config(map[string]string{})}
	for i := range 3 {
		id := fmt.Sprintf("p%d", i)
		h.cfg.Unions["work"][id] = "toolbar/U"
		b, bar := fakebrowser.New(), "1"
		file := profile(id)
		if i == 2 {
			b, bar = fakebrowser.NewFirefox(), "toolbar_____"
			file.Profile.Browser = "firefox"
		}
		m := &member{b: b, root: b.Add(bar, "U", ""), file: file}
		// Each member starts with its own bookmarks, and all with one the
		// same; the first rounds join them. With 30 nodes in the union and
		// few removes per burst, the wrong-tree guard has no reason to trip.
		n := 8
		if i == 0 {
			n = 12
		}
		for j := range n {
			b.Add(m.root, fmt.Sprintf("I%d", j), fmt.Sprintf("https://init/%s/%d", id, j))
		}
		b.Add(b.Add(m.root, "Shared", ""), "Same", "https://same/")
		h.ms = append(h.ms, m)
	}
	return h
}

func (h *hosts) Len() int { return len(h.ms) }

func (h *hosts) Member(i int) (*fakebrowser.Browser, string, []*db.Node) {
	m := h.ms[i]
	var rec []*db.Node
	if r := m.file.Unions["work"]; r != nil {
		rec = r.Children
	}
	return m.b, m.root, rec
}

func (h *hosts) Edited(int) {}

// Round reports a round that sent ops as a change too, so a quiet sweep is
// one that neither writes nor touches a browser.
func (h *hosts) Round(i int, fail func(int) bool) (bool, error) {
	m := h.ms[i]
	others := map[string]*db.DB{}
	for _, o := range h.ms {
		if o != m {
			others[o.file.Profile.ID] = o.file
		}
	}
	l, err := round.Build(m.b.Tree())
	if err != nil {
		return false, err
	}
	p := round.Make(round.Input{Config: h.cfg, Self: m.file, Others: others, Live: l, NewM: merge.NewM})
	if len(p.WrongTree) > 0 {
		return false, fmt.Errorf("member %d: %w", i, merge.ErrWrongTree)
	}
	if len(p.Skipped) > 0 {
		return false, fmt.Errorf("member %d: skipped %v", i, p.Skipped)
	}
	created := map[string]string{}
	for j, r := range m.b.Apply(p.Ops, fail) {
		if r.Error == "injected" {
			return false, nil // the batch stopped here; no file is written
		}
		if !r.OK {
			return false, fmt.Errorf("member %d: op %+v: %s", i, p.Ops[j], r.Error)
		}
		if p.Ops[j].Op == "create" {
			created[p.Ops[j].Ref] = r.ID
		}
	}
	if p.EnsureOnly {
		return false, fmt.Errorf("member %d: member folder missing", i)
	}
	next, changed, err := p.Finish(created)
	if err != nil {
		return false, fmt.Errorf("member %d: %w", i, err)
	}
	if changed {
		data, err := yaml.Marshal(next)
		if err != nil {
			return false, err
		}
		var d db.DB
		if err := yaml.Unmarshal(data, &d); err != nil {
			return false, err
		}
		m.file = &d
	}
	return changed || len(p.Ops) > 0, nil
}

// TestConvergence runs three hosts through random edits, closed profiles
// and batches that fail part-way, with removes kept under the wrong-tree
// guard. Run more seeds with -mirrormark.fuzz=20000.
func TestConvergence(t *testing.T) {
	failed := 0
	for seed := range fakebrowser.Seeds() {
		if err := fakebrowser.Fuzz(uint64(seed), newHosts(), 4); err != nil {
			t.Errorf("seed %d: %v", seed, err)
			if failed++; failed > 10 {
				t.Fatal("too many failing seeds")
			}
		}
	}
}
