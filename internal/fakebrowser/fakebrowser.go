// Package fakebrowser is an in-memory bookmarks tree that applies mirrormark
// ops the way the extension does. Tests use it in place of Chrome.
package fakebrowser

import (
	"errors"
	"slices"
	"strconv"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

type node struct {
	id, title, url string
	parent         *node
	kids           []*node
}

type Browser struct {
	nodes map[string]*node
	next  int
	refs  map[string]string
}

// New returns a browser with Chrome's three top-level folders:
// "1" Bookmarks bar, "2" Other bookmarks, "3" Mobile bookmarks.
func New() *Browser {
	b := &Browser{nodes: map[string]*node{}, refs: map[string]string{}}
	root := b.make(nil, "", "", -1)
	for _, t := range []string{"Bookmarks bar", "Other bookmarks", "Mobile bookmarks"} {
		b.make(root, t, "", -1)
	}
	return b
}

func (b *Browser) make(parent *node, title, url string, index int) *node {
	n := &node{id: strconv.Itoa(b.next), title: title, url: url, parent: parent}
	b.next++
	b.nodes[n.id] = n
	if parent != nil {
		b.insert(parent, n, index)
	}
	return n
}

func (b *Browser) insert(parent, n *node, index int) {
	if index < 0 || index > len(parent.kids) {
		index = len(parent.kids)
	}
	parent.kids = slices.Insert(parent.kids, index, n)
	n.parent = parent
}

func (b *Browser) detach(n *node) {
	p := n.parent
	p.kids = slices.DeleteFunc(p.kids, func(k *node) bool { return k == n })
}

func (b *Browser) forget(n *node) {
	delete(b.nodes, n.id)
	for _, k := range n.kids {
		b.forget(k)
	}
}

// Add appends a bookmark, or a folder when url is empty, and returns its id.
func (b *Browser) Add(parentID, title, url string) string {
	return b.make(b.nodes[parentID], title, url, -1).id
}
func (b *Browser) Rename(id, title string) { b.nodes[id].title = title }
func (b *Browser) SetURL(id, url string)   { b.nodes[id].url = url }
func (b *Browser) Move(id, parentID string) {
	n := b.nodes[id]
	b.detach(n)
	b.insert(b.nodes[parentID], n, -1)
}

// Remove removes a bookmark or an empty folder, and refuses a folder that
// holds anything, as chrome.bookmarks.remove does.
func (b *Browser) Remove(id string) error {
	n := b.nodes[id]
	if len(n.kids) > 0 {
		return errors.New("folder not empty")
	}
	b.detach(n)
	b.forget(n)
	return nil
}

// RemoveTree removes a node with everything inside it, as a user deleting a
// folder does.
func (b *Browser) RemoveTree(id string) {
	n := b.nodes[id]
	b.detach(n)
	b.forget(n)
}

// Find returns the id of the first child of parentID titled title, or "".
func (b *Browser) Find(parentID, title string) string {
	for _, k := range b.nodes[parentID].kids {
		if k.title == title {
			return k.id
		}
	}
	return ""
}

// Children returns the subtree under id as merge input: ID, Title, URL only.
func (b *Browser) Children(id string) []*db.Node {
	var conv func(ns []*node) []*db.Node
	conv = func(ns []*node) []*db.Node {
		var out []*db.Node
		for _, n := range ns {
			out = append(out, &db.Node{ID: n.id, Title: n.title, URL: n.url, Children: conv(n.kids)})
		}
		return out
	}
	return conv(b.nodes[id].kids)
}

// Lines lists the subtree under id as sorted "path url" lines, folders as
// "path/". Sibling order is ignored because mirrormark does not sync it.
func (b *Browser) Lines(id string) []string { return NodeLines(b.Children(id)) }

// NodeLines is Lines for a merge tree.
func NodeLines(ns []*db.Node) []string {
	var out []string
	var walk func(ns []*db.Node, prefix string)
	walk = func(ns []*db.Node, prefix string) {
		for _, n := range ns {
			p := prefix + "/" + n.Title
			if n.URL == "" {
				out = append(out, p+"/")
			} else {
				out = append(out, p+" "+n.URL)
			}
			walk(n.Children, p)
		}
	}
	walk(ns, "")
	slices.Sort(out)
	return out
}

// Apply runs ops one at a time and stops at the first that fails, as the
// extension does; the ops after it are answered "not run". fail, if not
// nil, makes op i fail without running, to simulate a browser error.
func (b *Browser) Apply(ops []merge.Op, fail func(i int) bool) []merge.OpResult {
	res := make([]merge.OpResult, len(ops))
	failed := false
	for i, op := range ops {
		switch {
		case failed:
			res[i] = merge.OpResult{Error: "not run"}
		case fail != nil && fail(i):
			res[i] = merge.OpResult{Error: "injected"}
		default:
			res[i] = b.apply(op)
		}
		failed = failed || !res[i].OK
	}
	return res
}

func (b *Browser) resolve(id string) *node {
	if r, ok := b.refs[id]; ok {
		id = r
	}
	return b.nodes[id]
}

func (b *Browser) apply(op merge.Op) merge.OpResult {
	stale := merge.OpResult{Error: "stale"}
	index := -1
	if op.Index != nil {
		index = *op.Index
	}
	switch op.Op {
	case "create":
		p := b.resolve(op.Parent)
		if p == nil {
			return merge.OpResult{Error: "no parent"}
		}
		url := ""
		if op.URL != nil {
			url = *op.URL
		}
		n := b.make(p, *op.Title, url, index)
		b.refs[op.Ref] = n.id
		return merge.OpResult{OK: true, ID: n.id}
	case "update":
		n := b.nodes[op.ID]
		if n == nil || (op.Expect.Title != nil && *op.Expect.Title != n.title) || (op.Expect.URL != nil && *op.Expect.URL != n.url) {
			return stale
		}
		if op.Title != nil {
			n.title = *op.Title
		}
		if op.URL != nil {
			n.url = *op.URL
		}
		return merge.OpResult{OK: true}
	case "move":
		n, p := b.nodes[op.ID], b.resolve(op.Parent)
		if n == nil || p == nil || n.parent.id != op.Expect.Parent {
			return stale
		}
		for a := p; a != nil; a = a.parent {
			if a == n {
				return merge.OpResult{Error: "move into itself"}
			}
		}
		b.detach(n)
		b.insert(p, n, index)
		return merge.OpResult{OK: true}
	case "remove":
		n := b.nodes[op.ID]
		if n == nil || *op.Expect.Title != n.title || *op.Expect.URL != n.url {
			return stale
		}
		if err := b.Remove(op.ID); err != nil {
			return merge.OpResult{Error: err.Error()}
		}
		return merge.OpResult{OK: true}
	}
	return merge.OpResult{Error: "unknown op " + op.Op}
}

// Tree returns the whole tree as the extension sends it.
func (b *Browser) Tree() []round.TreeNode {
	roots := map[string]string{"1": "toolbar", "2": "other", "3": "mobile"}
	var out []round.TreeNode
	var walk func(n *node, parent *string)
	walk = func(n *node, parent *string) {
		for i, k := range n.kids {
			out = append(out, round.TreeNode{ID: k.id, Parent: parent, Index: i, Title: k.title, URL: k.url, Root: roots[k.id]})
			walk(k, new(k.id))
		}
	}
	walk(b.nodes["0"], nil)
	return out
}
