// Package merge computes the round of one profile, P, for one union: a
// three-way merge of what P last published (base), what its browser holds now
// (ours) and the union's latest published state (theirs). It does no I/O.
//
// Nodes are matched across the three trees by their mirrormark id, m. Browser
// nodes get theirs from base by browser id. Title, URL and parent merge
// independently: a field changed on one side takes that side's value, a field
// changed the same way on both sides takes it once, and a field changed
// differently on both sides is a conflict. A bookmark conflict keeps both
// versions, each titled "<title> (<label of the profile that made it>)";
// theirs keeps its m and P's version gets a fresh one. The suffix field
// records the label mirrormark appended, so a second conflict replaces it
// rather than stacking another, and a title the user typed with parentheses
// is left alone. A folder is never copied, since that would duplicate its
// subtree: theirs wins each conflicting field and the conflict is reported.
//
// A node new on both sides with the same parent, title and URL is one node.
// Two new bookmarks with the same parent and title but different URLs
// conflict as above; here titles compare without a suffix mirrormark added,
// so a third profile's add of the same title is suffixed like the first two.
// Anything else new on one side is added to the other.
//
// Removing a node from the member folder is not a deletion: the node moves
// into a _deleted folder inside the member folder, and that move merges like
// any other edit, so it reaches every profile. Removing a node from inside
// _deleted is a purge, the one real deletion, and spreads to the other
// profiles; a purge loses to an edit made on the other side. Whatever the
// merge drops, a kept node never goes with it: a dropped parent comes back,
// and a node whose parent is gone everywhere moves to _deleted.
//
// When the merged parents form a cycle, the move that came from theirs is
// undone. A round is refused with ErrWrongTree when ours or theirs looks
// like an unloaded browser, a wrong binding or a damaged file rather than
// edits (see wrongTree). Deleting a folder, however large, is an edit.
//
// The result is the merged tree and the ops that turn P's browser into it.
// Sibling order is not merged: nodes the browser already holds keep their
// order, and other nodes go to their index in the tree they came from. A
// node the round would create is first matched against an identical new node
// already under the same parent in P's browser, so a retry after a round
// that failed part-way does not create anything twice.
package merge

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chriscz/mirrormark/internal/db"
)

// DeletedTitle names the folder, directly inside each member folder, that
// holds deleted bookmarks until the user empties it.
const DeletedTitle = "_deleted"

// ErrWrongTree means the round would move most of a member folder into
// _deleted or remove it in a way that is far more often a browser that has
// not loaded its bookmarks, a reset profile, a wrong binding or a damaged
// file than a real mass delete.
var ErrWrongTree = errors.New("db-mismatch: this round would move most of the member folder to " + DeletedTitle +
	" or purge it, which looks like an unloaded browser, a wrong binding or a damaged database file, so nothing was synced; " +
	"to delete that much, move the bookmarks into " + DeletedTitle + " yourself, or delete their folder; " +
	"if the database is wrong, move it aside and run mirrormark install again")

type Input struct {
	Base    []*db.Node // P's record; nil and HasBase false on P's first round
	HasBase bool
	Ours    []*db.Node // P's member folder now: ID, Title, URL only
	Theirs  []*db.Node // the union's latest record; nil if none yet
	RootID  string     // browser id of P's member folder
	Label   string     // P's label
	NewM    func() string
}

// Conflict records both versions of a node two profiles changed differently.
type Conflict struct {
	Folder       bool
	Ours, Theirs string // "title url" as each side had it
}

type Result struct {
	Tree      []*db.Node // the union as P's browser will hold it; ID "" until created
	Ops       []Op
	Conflicts []Conflict
}

// NewM returns a fresh mirrormark id.
func NewM() string {
	var b [6]byte
	rand.Read(b[:])
	return "m-" + hex.EncodeToString(b[:])
}

// last is past every real index, so a node placed there goes to the end.
const last = 1 << 30

type val struct {
	title, url, suffix, by string
	parent                 string // m of the parent; "" is the member folder
	folder                 bool
}

type entry struct {
	v     val
	index int
	id    string
}

// node is one entry of the merged union.
type node struct {
	val
	id          string // P's browser id; "" means create it
	index       int    // position among the source's siblings
	theirsMoved bool   // the parent came from theirs
	oursParent  string
}

type bnode struct {
	title, url, parent string
	index              int
}

type merger struct {
	in                 Input
	base, ours, theirs map[string]entry
	theirsKids         map[string][]string
	browser            map[string]bnode // P's browser before the round, by id
	browserOrder       []string         // children before their parent
	out, dropped       map[string]*node
	oursNew            map[string]bool // fresh m given to an unlinked add in ours
	retitle            map[string]val
	conflicts          []Conflict
	deleted            string
}

func Round(in Input) (Result, error) {
	m := &merger{in: in, browser: map[string]bnode{}, out: map[string]*node{},
		dropped: map[string]*node{}, retitle: map[string]val{}, theirsKids: map[string][]string{}, oursNew: map[string]bool{}}
	m.indexBrowser(in.Ours, in.RootID)
	m.base = flatten(in.Base)
	m.theirs = flatten(in.Theirs)
	for _, k := range slices.Sorted(maps.Keys(m.theirs)) {
		p := m.theirs[k].v.parent
		m.theirsKids[p] = append(m.theirsKids[p], k)
	}
	for _, kids := range m.theirsKids {
		slices.SortFunc(kids, func(a, b string) int { return m.theirs[a].index - m.theirs[b].index })
	}
	ours := clone(in.Ours)
	m.assign(ours)
	m.linkAdds("", ours)
	m.ours = flatten(ours)

	keys := map[string]bool{}
	for _, s := range []map[string]entry{m.base, m.ours, m.theirs} {
		for k := range s {
			keys[k] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		m.mergeOne(k)
	}
	if m.wrongTree() {
		return Result{}, ErrWrongTree
	}
	// Undoing a move can put a node back under a parent the merge dropped,
	// so parents are kept again until neither step changes anything.
	for m.resurrect(); m.breakCycles(); m.resurrect() {
	}
	kids := map[string][]string{}
	for _, k := range slices.Sorted(maps.Keys(m.out)) {
		p := m.out[k].parent
		kids[p] = append(kids[p], k)
	}
	m.dedupe(kids)
	tree := m.build("", kids)
	if n := count(tree); n != len(m.out) {
		// A node that hangs from nothing would not be in the tree, so its
		// browser node would be removed. Refuse rather than lose it.
		return Result{}, fmt.Errorf("merge: %d nodes do not reach the member folder", len(m.out)-n)
	}
	return Result{Tree: tree, Ops: m.ops(tree), Conflicts: m.conflicts}, nil
}

func count(ns []*db.Node) int {
	n := len(ns)
	for _, c := range ns {
		n += count(c.Children)
	}
	return n
}

func clone(ns []*db.Node) []*db.Node {
	var out []*db.Node
	for _, n := range ns {
		c := *n
		c.Children = clone(n.Children)
		out = append(out, &c)
	}
	return out
}

func flatten(ns []*db.Node) map[string]entry {
	out := map[string]entry{}
	var walk func(ns []*db.Node, parent string)
	walk = func(ns []*db.Node, parent string) {
		for i, n := range ns {
			out[n.M] = entry{v: val{title: n.Title, url: n.URL, suffix: n.Suffix, by: n.By, parent: parent, folder: n.URL == ""}, index: i, id: n.ID}
			walk(n.Children, n.M)
		}
	}
	walk(ns, "")
	return out
}

func (m *merger) indexBrowser(ns []*db.Node, parent string) {
	for i, n := range ns {
		m.browser[n.ID] = bnode{title: n.Title, url: n.URL, parent: parent, index: i}
		m.indexBrowser(n.Children, n.ID)
		m.browserOrder = append(m.browserOrder, n.ID)
	}
}

// assign gives P's browser nodes their m from base, matched by browser id.
func (m *merger) assign(ns []*db.Node) {
	byID := map[string]string{}
	for k, e := range m.base {
		byID[e.id] = k
	}
	var walk func(ns []*db.Node)
	walk = func(ns []*db.Node) {
		for _, n := range ns {
			if k, ok := byID[n.ID]; ok {
				b := m.base[k].v
				n.M, n.By = k, b.by
				if b.suffix != "" && strings.HasSuffix(n.Title, " ("+b.suffix+")") {
					n.Suffix = b.suffix
				}
			}
			walk(n.Children)
		}
	}
	walk(ns)
}

// linkAdds gives each node new in ours an m: theirs' m for an identical add
// under the same parent, a fresh one otherwise. Top-down, so a linked
// folder's new children can link too.
func (m *merger) linkAdds(parent string, ns []*db.Node) {
	var fresh []string
	for _, k := range m.theirsKids[parent] {
		if _, inBase := m.base[k]; !inBase {
			fresh = append(fresh, k)
		}
	}
	for _, n := range ns {
		if n.M == "" {
			n.M = m.matchAdd(n, &fresh)
		}
		m.linkAdds(n.M, n.Children)
	}
}

func (m *merger) matchAdd(n *db.Node, fresh *[]string) string {
	for i, k := range *fresh {
		if t := m.theirs[k].v; t.title == n.Title && t.url == n.URL {
			*fresh = slices.Delete(*fresh, i, i+1)
			n.By, n.Suffix = t.by, t.suffix
			return k
		}
	}
	if n.URL != "" {
		// Titles compare without the suffixes mirrormark added: theirs may be
		// a copy from another conflict, and ours may carry our suffix from a
		// round whose update landed but whose file was never written.
		ours := strings.TrimSuffix(n.Title, " ("+m.in.Label+")")
		for i, k := range *fresh {
			t := m.theirs[k].v
			if bt := bare(t); t.folder || bt != n.Title && bt != ours {
				continue
			}
			*fresh = slices.Delete(*fresh, i, i+1)
			m.conflicts = append(m.conflicts, Conflict{Ours: n.Title + " " + n.URL, Theirs: t.title + " " + t.url})
			m.retitle[k] = suffixed(t, t.by)
			o := suffixed(val{title: ours}, m.in.Label)
			n.Title, n.Suffix, n.By = o.title, o.suffix, m.in.Label
			return m.in.NewM()
		}
	}
	n.By = m.in.Label
	k := m.in.NewM()
	m.oursNew[k] = true
	return k
}

// bare is v's title without the suffix mirrormark added.
func bare(v val) string {
	if v.suffix == "" {
		return v.title
	}
	return strings.TrimSuffix(v.title, " ("+v.suffix+")")
}

// suffixed retitles v as "<title> (<label>)", replacing a suffix mirrormark
// added before instead of stacking a second one.
func suffixed(v val, label string) val {
	v.title = bare(v) + " (" + label + ")"
	v.suffix = label
	return v
}

func same(a, b val) bool { return a.title == b.title && a.url == b.url && a.parent == b.parent }

func (m *merger) put(k string, v val, id string, index int, theirsMoved bool) *node {
	n := &node{val: v, id: id, index: index, theirsMoved: theirsMoved}
	m.out[k] = n
	return n
}

func (m *merger) mergeOne(k string) {
	b, inB := m.base[k]
	o, inO := m.ours[k]
	t, inT := m.theirs[k]
	switch {
	case inB && !inO && !inT:
		// gone everywhere
	case inB && !inO:
		if m.inDeleted(k) { // P purged it
			if same(b.v, t.v) {
				m.dropped[k] = &node{val: t.v, index: t.index}
				return
			}
			m.put(k, t.v, "", t.index, true)
			return
		}
		// P deleted it: that is a move into _deleted, merged like any edit.
		vo := b.v
		if _, ok := m.ours[b.v.parent]; ok || b.v.parent == "" {
			vo.parent = m.deletedM()
		}
		m.merge3(k, b.v, vo, t.v, "", last, t.index)
	case inB && !inT: // another member purged it
		if same(b.v, o.v) {
			m.dropped[k] = &node{val: o.v, id: o.id, index: o.index}
			return
		}
		v := o.v
		v.by = m.in.Label
		m.put(k, v, o.id, o.index, false)
	case inB:
		m.merge3(k, b.v, o.v, t.v, o.id, o.index, t.index)
	case inO && inT: // the same add on both sides, linked
		m.put(k, o.v, o.id, o.index, false)
	case inO:
		m.put(k, o.v, o.id, o.index, false)
	default: // added by another member
		v := t.v
		if r, ok := m.retitle[k]; ok {
			v = r
		}
		m.put(k, v, "", t.index, true)
	}
}

func (m *merger) merge3(k string, b, o, t val, id string, oIndex, tIndex int) {
	const fromBase, fromOurs, fromTheirs = 0, 1, 2
	field := func(bf, of, tf string) (string, int, bool) {
		switch {
		case of == bf && tf == bf:
			return bf, fromBase, true
		case tf == bf:
			return of, fromOurs, true
		case of == bf:
			return tf, fromTheirs, true
		case of == tf:
			return of, fromOurs, true
		}
		return "", 0, false
	}
	title, ft, ok1 := field(b.title, o.title, t.title)
	url, fu, ok2 := field(b.url, o.url, t.url)
	parent, fp, ok3 := field(b.parent, o.parent, t.parent)
	if !ok1 || !ok2 || !ok3 {
		c := Conflict{Folder: b.folder, Ours: o.title + " " + o.url, Theirs: t.title + " " + t.url}
		m.conflicts = append(m.conflicts, c)
		if !b.folder {
			// Both survive: theirs keeps m, P's browser node becomes the copy.
			m.put(k, suffixed(t, t.by), "", tIndex, true)
			if o.suffix == "" && o.title == suffixed(b, m.in.Label).title {
				// Our copy's retitle landed in a round that wrote no file.
				o.suffix = m.in.Label
			}
			ov := suffixed(o, m.in.Label)
			ov.by = m.in.Label
			m.put(m.in.NewM(), ov, id, oIndex, false)
			return
		}
		// Folders are never copied, because a copy would duplicate the
		// subtree. Theirs wins: it is already in the other profiles.
		if !ok1 {
			title, ft = t.title, fromTheirs
		}
		if !ok2 {
			url, fu = t.url, fromTheirs
		}
		if !ok3 {
			parent, fp = t.parent, fromTheirs
		}
	}
	r := val{title: title, url: url, parent: parent, folder: b.folder, by: b.by,
		suffix: [3]string{b.suffix, o.suffix, t.suffix}[ft]}
	switch {
	case ft == fromOurs || fu == fromOurs || fp == fromOurs:
		r.by = m.in.Label
	case ft == fromTheirs || fu == fromTheirs || fp == fromTheirs:
		r.by = t.by
	}
	index := oIndex
	if fp == fromTheirs {
		index = tIndex
	}
	n := m.put(k, r, id, index, fp == fromTheirs)
	n.oursParent = o.parent
}

// wrongTree reports whether ours or theirs looks like the wrong tree rather
// than edits, once base holds more than five nodes: ours is empty, as in a
// browser that has not loaded its bookmarks; or most of base is gone from
// ours and most of ours is not in base, as in a profile bound to another
// profile's database; or more than half of base would go. For that count a
// node gone from ours counts only where its parent is still there, so
// deleting a folder counts once however much it holds, and a node live in
// base and gone from theirs counts each time, since theirs is a whole
// published state and lacks a node only if it was purged or the file is
// damaged.
func (m *merger) wrongTree() bool {
	if !m.in.HasBase || len(m.base) <= 5 {
		return false
	}
	if len(m.ours) == 0 {
		return true
	}
	gone, missing, fresh := 0, 0, 0
	for k, e := range m.base {
		_, inO := m.ours[k]
		_, inT := m.theirs[k]
		_, parentInO := m.ours[e.v.parent]
		if !inO {
			missing++
		}
		if !inO && (parentInO || e.v.parent == "") || !inT && !m.inDeleted(k) {
			gone++
		}
	}
	for k := range m.ours {
		if _, ok := m.base[k]; !ok {
			fresh++
		}
	}
	return 2*gone > len(m.base) || 2*missing > len(m.base) && 2*fresh > len(m.ours)
}

// inDeleted reports whether base node k is _deleted or inside it.
func (m *merger) inDeleted(k string) bool {
	for range len(m.base) + 1 { // bounded, in case a hand edit made a loop
		e, ok := m.base[k]
		if !ok {
			return false
		}
		if e.v.parent == "" && e.v.folder && e.v.title == DeletedTitle {
			return true
		}
		k = e.v.parent
	}
	return false
}

// deletedM returns the m of the _deleted folder, choosing a fresh one if no
// side has it. resurrect creates the folder if the merge did not keep it.
func (m *merger) deletedM() string {
	if m.deleted != "" {
		return m.deleted
	}
	for _, s := range []map[string]entry{m.ours, m.theirs, m.base} {
		for _, k := range slices.Sorted(maps.Keys(s)) {
			if v := s[k].v; v.parent == "" && v.folder && v.title == DeletedTitle {
				m.deleted = k
				return k
			}
		}
	}
	m.deleted = m.in.NewM()
	return m.deleted
}

// resurrect keeps every parent of a kept node: a dropped parent comes back,
// a missing _deleted is created, and anything else missing sends the node
// to _deleted. A merge never loses a node by losing its parent.
func (m *merger) resurrect() {
	for changed := true; changed; {
		changed = false
		for _, k := range slices.Sorted(maps.Keys(m.out)) {
			n := m.out[k]
			if n.parent == "" {
				continue
			}
			if _, ok := m.out[n.parent]; ok {
				continue
			}
			changed = true
			switch d, ok := m.dropped[n.parent]; {
			case ok:
				m.out[n.parent] = d
				delete(m.dropped, n.parent)
			case n.parent == m.deleted:
				m.put(m.deleted, val{title: DeletedTitle, folder: true, by: m.in.Label}, "", last, false)
			default:
				n.parent, n.index = m.deletedM(), last
			}
		}
	}
}

// breakCycles undoes, in each cycle, a move that came from theirs. A cycle
// needs one move from each side (A moves X into Y, B moves Y into X). It
// reports whether it changed anything; an undone move can need a parent
// that resurrect has to bring back.
func (m *merger) breakCycles() bool {
	changed := false
	for found := true; found; {
		found = false
		for _, k := range slices.Sorted(maps.Keys(m.out)) {
			seen := map[string]bool{}
			p := k
			for p != "" && !seen[p] {
				seen[p] = true
				n, ok := m.out[p]
				if !ok { // a missing parent, left for resurrect
					p = ""
					break
				}
				p = n.parent
			}
			if p == "" {
				continue
			}
			found, changed = true, true
			c := p
			for !m.out[c].theirsMoved {
				c = m.out[c].parent
				if c == p { // no move from theirs: cannot happen with valid input
					m.out[p].parent = m.deletedM()
					break
				}
			}
			if n := m.out[c]; n.theirsMoved {
				n.parent, n.theirsMoved = n.oursParent, false
			}
			break
		}
	}
	return changed
}

// dedupe links each node this round would create to an identical unlinked
// add in P's browser under the same parent, top-down. After a round that
// failed part-way, the creates that landed show up as such adds; linking
// them keeps the retry from creating them twice.
func (m *merger) dedupe(kids map[string][]string) {
	queue := []string{""}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, k := range slices.Clone(kids[p]) {
			n, ok := m.out[k]
			if !ok || n.id != "" {
				continue
			}
			i := slices.IndexFunc(kids[p], func(j string) bool {
				o := m.out[j]
				return m.oursNew[j] && o.title == n.title && o.url == n.url
			})
			if i < 0 {
				continue
			}
			j := kids[p][i]
			o := m.out[j]
			n.id, n.index = o.id, o.index
			for _, c := range kids[j] {
				m.out[c].parent = k
			}
			kids[k] = append(kids[k], kids[j]...)
			delete(kids, j)
			delete(m.out, j)
			delete(m.oursNew, j)
			kids[p] = slices.Delete(kids[p], i, i+1)
		}
		queue = append(queue, kids[p]...)
	}
}

// build orders each folder's children as P's browser will hold them: the
// nodes the browser already holds there in its order, the others at their
// source index among them. ops places each node after its tree sibling, so
// the browser ends up in exactly this order and the next round sees no
// change.
func (m *merger) build(parent string, kids map[string][]string) []*db.Node {
	pid := m.in.RootID
	if parent != "" {
		pid = m.out[parent].id
	}
	var ordered, rest []string
	for _, k := range kids[parent] {
		if id := m.out[k].id; id != "" && pid != "" && m.browser[id].parent == pid {
			ordered = append(ordered, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortStableFunc(ordered, func(a, b string) int { return m.browser[m.out[a].id].index - m.browser[m.out[b].id].index })
	slices.SortStableFunc(rest, func(a, b string) int { return m.out[a].index - m.out[b].index })
	for _, k := range rest {
		ordered = slices.Insert(ordered, min(m.out[k].index, len(ordered)), k)
	}
	var out []*db.Node
	for _, k := range ordered {
		n := m.out[k]
		out = append(out, &db.Node{Title: n.title, URL: n.url, ID: n.id, M: k, By: n.by, Suffix: n.suffix, Children: m.build(k, kids)})
	}
	return out
}

// ops turns P's browser into tree: creates and moves top-down, so a parent
// exists before its children, then updates, then removes last, so nothing
// still needed is inside a removed folder. Every node that goes gets its own
// remove, children before their folder, because a remove is non-recursive:
// if a move out of a folder failed, removing the folder fails too instead
// of taking the node with it.
func (m *merger) ops(tree []*db.Node) []Op {
	var seq, upd, rem []Op
	kept := map[string]bool{}
	// kids tracks each folder's children as the ops leave them, by browser
	// id or, for a created node, its Ref. Each node goes right after its
	// tree sibling; a node the browser holds before that sibling may still
	// leave later in the batch, so the tree index alone could be off.
	kids := map[string][]string{}
	for _, id := range m.browserOrder {
		p := m.browser[id].parent
		kids[p] = append(kids[p], id)
	}
	key := func(n *db.Node) string { return cmp.Or(n.ID, n.M) }
	var walk func(parent string, ns []*db.Node)
	walk = func(parent string, ns []*db.Node) {
		for i, n := range ns {
			at := 0
			if i > 0 {
				at = slices.Index(kids[parent], key(ns[i-1])) + 1
			}
			if n.ID == "" {
				op := Op{Op: "create", Ref: n.M, Parent: parent, Index: new(at), Title: new(n.Title)}
				if n.URL != "" {
					op.URL = new(n.URL)
				}
				seq = append(seq, op)
				kids[parent] = slices.Insert(kids[parent], at, n.M)
				walk(n.M, n.Children)
				continue
			}
			kept[n.ID] = true
			b := m.browser[n.ID]
			if b.parent != parent {
				seq = append(seq, Op{Op: "move", ID: n.ID, Parent: parent, Index: new(at), Expect: &Expect{Parent: b.parent}})
				kids[b.parent] = slices.DeleteFunc(kids[b.parent], func(id string) bool { return id == n.ID })
				kids[parent] = slices.Insert(kids[parent], at, n.ID)
			}
			if b.title != n.Title || b.url != n.URL {
				op := Op{Op: "update", ID: n.ID, Expect: &Expect{Title: new(b.title), URL: new(b.url)}}
				if b.title != n.Title {
					op.Title = new(n.Title)
				}
				if b.url != n.URL {
					op.URL = new(n.URL)
				}
				upd = append(upd, op)
			}
			walk(n.ID, n.Children)
		}
	}
	walk(m.in.RootID, tree)
	for _, id := range m.browserOrder {
		if b := m.browser[id]; !kept[id] {
			rem = append(rem, Op{Op: "remove", ID: id, Expect: &Expect{Title: new(b.title), URL: new(b.url)}})
		}
	}
	return slices.Concat(seq, upd, rem)
}
