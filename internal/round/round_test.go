package round_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

func profile(id string) *db.DB {
	return &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: "chrome/" + id, Browser: "chrome"}}
}

func config(paths map[string]string) *db.Config {
	return &db.Config{Format: 1, Unions: map[string]map[string]string{"work": paths}}
}

func live(t *testing.T, b *fakebrowser.Browser) *round.Live {
	t.Helper()
	l, err := round.Build(b.Tree())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// apply runs a plan against b and returns the created ids by ref.
func apply(t *testing.T, b *fakebrowser.Browser, p round.Plan) map[string]string {
	t.Helper()
	created := map[string]string{}
	for i, r := range b.Apply(p.Ops, nil) {
		if !r.OK {
			t.Fatalf("op %+v: %s", p.Ops[i], r.Error)
		}
		if p.Ops[i].Op == "create" {
			created[p.Ops[i].Ref] = r.ID
		}
	}
	return created
}

func TestMissingMemberFolderIsCreatedFirst(t *testing.T) {
	b := fakebrowser.New()
	a := profile("a")
	cfg := config(map[string]string{"a": "toolbar/Work/Sub"})
	p := round.Make(round.Input{Config: cfg, Self: a, Live: live(t, b), NewM: merge.NewM})
	if !p.EnsureOnly || len(p.Ops) != 2 {
		t.Fatalf("plan %+v, want two folder creates", p)
	}
	apply(t, b, p)
	if got := b.Lines("1"); !slices.Equal(got, []string{"/Work/", "/Work/Sub/"}) {
		t.Fatalf("bar %q", got)
	}
}

// A plan that only creates member folders reports no conflicts: the next
// round makes the merges' ops again and reports theirs.
func TestEnsureOnlyPlanReportsNoConflicts(t *testing.T) {
	defer round.SetMerge(func(merge.Input) (merge.Result, error) {
		return merge.Result{Conflicts: []merge.Conflict{{Ours: "A https://a/", Theirs: "B https://a/"}}}, nil
	})()
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	a := profile("a")
	a.Unions = map[string]*db.Member{"work": {Seq: 1, Path: "toolbar/Work", ID: w}}
	cfg := &db.Config{Format: 1, Unions: map[string]map[string]string{
		"work":    {"a": "toolbar/Work"},
		"reading": {"a": "other/Reading"},
	}}
	p := round.Make(round.Input{Config: cfg, Self: a, Live: live(t, b), NewM: merge.NewM})
	if !p.EnsureOnly || len(p.Ops) != 1 {
		t.Fatalf("plan %+v, want one folder create", p)
	}
	if len(p.Conflicts) != 0 {
		t.Fatalf("an ensure-only plan reported conflicts: %+v", p.Conflicts)
	}
}

func TestFirstRoundRecordsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	cfg := config(map[string]string{"a": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: a, Live: live(t, b), NewM: merge.NewM})
	next, changed, err := p.Finish(apply(t, b, p))
	if err != nil || !changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	rec := next.Unions["work"]
	if rec.Seq != 1 || rec.Path != "toolbar/Work" || rec.ID != w || len(rec.Children) != 1 || rec.Children[0].M == "" {
		t.Fatalf("record %+v", rec)
	}
	// A second round with nothing new writes nothing.
	p = round.Make(round.Input{Config: cfg, Self: next, Live: live(t, b), NewM: merge.NewM})
	if _, changed, _ := p.Finish(apply(t, b, p)); changed || len(p.Ops) != 0 {
		t.Fatalf("quiet round changed %v ops %+v", changed, p.Ops)
	}
}

func TestUnreadableMemberSkipsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	cfg := config(map[string]string{"a": "toolbar/Work", "b": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: profile("a"), Others: map[string]*db.DB{}, Live: live(t, b), NewM: merge.NewM})
	if !strings.Contains(p.Skipped["work"], "b") || len(p.Ops) != 0 {
		t.Fatalf("plan %+v", p)
	}
}

func TestReplacedMemberFolderIsSkipped(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	a := profile("a")
	a.Unions = map[string]*db.Member{"work": {Seq: 4, Path: "toolbar/Work", ID: "999"}}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if p.Skipped["work"] == "" {
		t.Fatalf("plan %+v, want work skipped", p)
	}
	next, changed, _ := p.Finish(nil)
	if changed || next.Unions["work"].Seq != 4 {
		t.Fatalf("skipped union must keep its record: changed %v %+v", changed, next.Unions["work"])
	}
}

func TestChangedConfigPathMakesANewMember(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("2", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	a.Unions = map[string]*db.Member{"work": {Seq: 4, Path: "toolbar/Work", ID: "999",
		Children: []*db.Node{{Title: "Old", URL: "https://old/", ID: "998", M: "m-old"}}}}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "other/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	next, changed, err := p.Finish(apply(t, b, p))
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	// No base: the old record is not read as deletions, and theirs is the
	// old record (highest seq), so its bookmark joins the new folder.
	if got := b.Lines(w); !slices.Equal(got, []string{"/GitHub https://github.com/", "/Old https://old/"}) {
		t.Fatalf("folder %q", got)
	}
	if next.Unions["work"].Seq != 5 {
		t.Fatalf("seq %d, want 5", next.Unions["work"].Seq)
	}
}

func TestTheirsIsTheHighestSeqRecord(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	a, x, y := profile("a"), profile("x"), profile("y")
	x.Unions = map[string]*db.Member{"work": {Seq: 7, Path: "toolbar/Work", Children: []*db.Node{{Title: "New", URL: "https://new/", M: "m-1"}}}}
	y.Unions = map[string]*db.Member{"work": {Seq: 3, Path: "toolbar/Work", Children: []*db.Node{{Title: "Stale", URL: "https://stale/", M: "m-2"}}}}
	cfg := config(map[string]string{"a": "toolbar/Work", "x": "toolbar/Work", "y": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: a, Others: map[string]*db.DB{"x": x, "y": y}, Live: live(t, b), NewM: merge.NewM})
	next, _, _ := p.Finish(apply(t, b, p))
	if got := b.Lines(w); !slices.Equal(got, []string{"/New https://new/"}) {
		t.Fatalf("folder %q", got)
	}
	if next.Unions["work"].Seq != 8 {
		t.Fatalf("seq %d, want 8", next.Unions["work"].Seq)
	}
}

func TestFinishFailsWithoutCreatedIDs(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	x := profile("x")
	x.Unions = map[string]*db.Member{"work": {Seq: 1, Path: "toolbar/Work", Children: []*db.Node{{Title: "N", URL: "https://n/", M: "m-1"}}}}
	cfg := config(map[string]string{"a": "toolbar/Work", "x": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: profile("a"), Others: map[string]*db.DB{"x": x}, Live: live(t, b), NewM: merge.NewM})
	if _, _, err := p.Finish(map[string]string{}); err == nil {
		t.Fatal("Finish accepted a create without an id")
	}
}

func TestFailedMergeSkipsTheUnion(t *testing.T) {
	defer round.SetMerge(func(merge.Input) (merge.Result, error) {
		return merge.Result{}, errors.New("merge: 1 nodes do not reach the member folder")
	})()
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	old := &db.Member{Seq: 4, Path: "toolbar/Work", ID: w, Children: []*db.Node{{Title: "GitHub", URL: "https://github.com/", ID: "5", M: "m-1"}}}
	a.Unions = map[string]*db.Member{"work": old}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if !strings.Contains(p.Skipped["work"], "do not reach") || len(p.Ops) != 0 || len(p.WrongTree) != 0 {
		t.Fatalf("plan %+v", p)
	}
	next, changed, err := p.Finish(nil)
	if err != nil || changed || next.Unions["work"] != old {
		t.Fatalf("a failed merge must keep the record: changed %v err %v %+v", changed, err, next.Unions["work"])
	}
}

func TestWrongTreeSkipsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	a := profile("a")
	old := &db.Member{Seq: 4, Path: "toolbar/Work", ID: w}
	for i := range 6 {
		old.Children = append(old.Children, &db.Node{Title: "B", URL: fmt.Sprintf("https://b/%d", i), ID: fmt.Sprint(100 + i), M: fmt.Sprintf("m-%d", i)})
	}
	a.Unions = map[string]*db.Member{"work": old}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if !slices.Equal(p.WrongTree, []string{"work"}) || p.Skipped["work"] == "" || len(p.Ops) != 0 {
		t.Fatalf("plan %+v", p)
	}
	if next, changed, _ := p.Finish(nil); changed || next.Unions["work"] != old {
		t.Fatalf("the guard must keep the record: changed %v", changed)
	}
}

// A Firefox member's path may start with menu, the Bookmarks Menu.
func TestFirefoxMenuPath(t *testing.T) {
	b := fakebrowser.NewFirefox()
	f := profile("f")
	f.Profile.Browser = "firefox"
	p := round.Make(round.Input{Config: config(map[string]string{"f": "menu/Work"}), Self: f, Live: live(t, b), NewM: merge.NewM})
	if !p.EnsureOnly || len(p.Ops) != 1 || p.Ops[0].Parent != "menu________" {
		t.Fatalf("plan %+v, want one create in menu________", p)
	}
	apply(t, b, p)
	if got := b.Lines("menu________"); !slices.Equal(got, []string{"/Work/"}) {
		t.Fatalf("menu %q", got)
	}
}
