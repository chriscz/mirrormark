package round

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
)

// mergeRound is merge.Round; a test replaces it to make a merge fail.
var mergeRound = merge.Round

type Input struct {
	Config *db.Config
	Self   *db.DB
	Others map[string]*db.DB // other members' files by profile id; absent = unreadable
	Live   *Live
	NewM   func() string
}

// Plan is what one round should do. If EnsureOnly, Ops only create missing
// member folders and the round must not write: the next tree shows them.
type Plan struct {
	Ops        []merge.Op
	EnsureOnly bool
	Conflicts  []merge.Conflict
	Skipped    map[string]string // union → why it was not synced
	WrongTree  []string          // unions the wrong-tree guard refused

	self *db.DB
	next *db.DB
	seq  map[string]int64
}

func Make(in Input) Plan {
	p := Plan{Skipped: map[string]string{}, self: in.Self, seq: map[string]int64{}}
	next := *in.Self
	next.Unions = map[string]*db.Member{}
	var ensure []merge.Op
	me := in.Self.Profile.ID
	for _, name := range slices.Sorted(maps.Keys(in.Config.Unions)) {
		members := in.Config.Unions[name]
		path, ok := members[me]
		if !ok {
			continue
		}
		skip := func(reason string) {
			p.Skipped[name] = reason
			if old := in.Self.Unions[name]; old != nil {
				next.Unions[name] = old
			}
		}
		// A record made for another path is not a base: the config points
		// somewhere new, so this is a new member. It still counts as a
		// published state of the union, so it can be theirs.
		var records []*db.Member
		rec := in.Self.Unions[name]
		if rec != nil {
			records = append(records, rec)
			if rec.Path != path {
				rec = nil
			}
		}
		unreadable := ""
		for _, id := range slices.Sorted(maps.Keys(members)) {
			if id == me {
				continue
			}
			o, ok := in.Others[id]
			if !ok {
				unreadable = id
				break
			}
			if r := o.Unions[name]; r != nil {
				records = append(records, r)
			}
		}
		if unreadable != "" {
			// Never treat a member we cannot read as empty.
			skip(fmt.Sprintf("cannot read the database file of member %s", unreadable))
			continue
		}
		segs, err := db.SplitPath(path)
		if err != nil {
			skip(err.Error())
			continue
		}
		folder, n := in.Live.resolve(segs)
		if n < len(segs) {
			if rec != nil {
				// A folder that disappeared never means "everything was deleted".
				skip(fmt.Sprintf("member folder %s is missing; restore it or change the path in config.yaml", path))
				continue
			}
			if folder == nil {
				skip(fmt.Sprintf("no top-level folder %q", segs[0]))
				continue
			}
			parent := folder.ID
			for i := n; i < len(segs); i++ {
				ref := fmt.Sprintf("path:%s:%d", name, i)
				ensure = append(ensure, merge.Op{Op: "create", Ref: ref, Parent: parent, Title: new(segs[i])})
				parent = ref
			}
			continue
		}
		if rec != nil && rec.ID != folder.ID {
			skip(fmt.Sprintf("%s is a different folder than before; restore it or change the path in config.yaml", path))
			continue
		}
		var theirs *db.Member
		var top int64
		for _, r := range records {
			if theirs == nil || r.Seq > theirs.Seq {
				theirs = r
			}
			top = max(top, r.Seq)
		}
		mi := merge.Input{HasBase: rec != nil, Ours: folder.children(), RootID: folder.ID, Label: in.Self.Profile.Label, NewM: in.NewM}
		if rec != nil {
			mi.Base = rec.Children
		}
		if theirs != nil {
			mi.Theirs = theirs.Children
		}
		res, err := mergeRound(mi)
		if err != nil {
			// Never go on with an empty result: it would publish an
			// empty union to every member.
			if errors.Is(err, merge.ErrWrongTree) {
				p.WrongTree = append(p.WrongTree, name)
			}
			skip(err.Error())
			continue
		}
		p.Ops = append(p.Ops, res.Ops...)
		p.Conflicts = append(p.Conflicts, res.Conflicts...)
		p.seq[name] = top + 1
		next.Unions[name] = &db.Member{Path: path, ID: folder.ID, Children: res.Tree}
	}
	if len(ensure) > 0 {
		p.Ops, p.EnsureOnly = ensure, true
	}
	p.next = &next
	return p
}

// Finish fills in the ids of created nodes and reports whether the database
// file changes. Only changed union records get a new seq, so a round that
// changes nothing writes nothing and the rounds stop.
func (p Plan) Finish(created map[string]string) (*db.DB, bool, error) {
	changed := false
	for name := range p.self.Unions {
		if _, ok := p.next.Unions[name]; !ok {
			changed = true
		}
	}
	for name, rec := range p.next.Unions {
		old := p.self.Unions[name]
		if rec == old {
			continue // skipped: kept as it was
		}
		if err := fillIDs(rec.Children, created); err != nil {
			return nil, false, err
		}
		if old != nil && old.Path == rec.Path && old.ID == rec.ID && db.EqualNodes(old.Children, rec.Children) {
			rec.Seq = old.Seq
			continue
		}
		rec.Seq = p.seq[name]
		changed = true
	}
	return p.next, changed, nil
}

func fillIDs(ns []*db.Node, created map[string]string) error {
	for _, n := range ns {
		if n.ID == "" {
			id, ok := created[n.M]
			if !ok {
				return fmt.Errorf("no browser id for created node %s %q", n.M, n.Title)
			}
			n.ID = id
		}
		if err := fillIDs(n.Children, created); err != nil {
			return err
		}
	}
	return nil
}
