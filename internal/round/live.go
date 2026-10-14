// Package round plans one host round: for every union the profile belongs
// to, which records to merge, which ops to send and what to write. No I/O.
package round

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/chriscz/mirrormark/internal/db"
)

// TreeNode is one bookmark or folder in the extension's tree message.
// Top-level folders have Parent nil and Root set to toolbar, other or mobile.
type TreeNode struct {
	ID      string  `json:"id"`
	Parent  *string `json:"parent"`
	Index   int     `json:"index"`
	Title   string  `json:"title"`
	URL     string  `json:"url,omitempty"`
	Root    string  `json:"root,omitempty"`
	Syncing bool    `json:"syncing,omitempty"`
}

// Live is the browser's whole bookmarks tree.
type Live struct {
	top  []*liveNode
	byID map[string]*liveNode
}

type liveNode struct {
	TreeNode
	kids []*liveNode
}

func Build(nodes []TreeNode) (*Live, error) {
	l := &Live{byID: map[string]*liveNode{}}
	for _, n := range nodes {
		if _, dup := l.byID[n.ID]; dup {
			return nil, fmt.Errorf("tree: duplicate id %q", n.ID)
		}
		l.byID[n.ID] = &liveNode{TreeNode: n}
	}
	for _, n := range nodes {
		ln := l.byID[n.ID]
		if n.Parent == nil {
			l.top = append(l.top, ln)
			continue
		}
		p, ok := l.byID[*n.Parent]
		if !ok {
			return nil, fmt.Errorf("tree: node %q has unknown parent %q", n.ID, *n.Parent)
		}
		p.kids = append(p.kids, ln)
	}
	for _, n := range l.byID {
		slices.SortStableFunc(n.kids, func(a, b *liveNode) int { return cmp.Compare(a.Index, b.Index) })
	}
	return l, nil
}

// resolve follows segs and returns the deepest folder reached and how many
// segments matched. The first segment names a top-level folder by keyword
// or title; account folders (syncing) win over local ones of the same name.
func (l *Live) resolve(segs []string) (*liveNode, int) {
	var cur *liveNode
	for i, s := range segs {
		var next *liveNode
		if i == 0 {
			for _, t := range l.top {
				if (t.Root == s || t.Title == s) && (next == nil || t.Syncing && !next.Syncing) {
					next = t
				}
			}
		} else {
			for _, c := range cur.kids {
				if c.URL == "" && c.Title == s {
					next = c
					break
				}
			}
		}
		if next == nil {
			return cur, i
		}
		cur = next
	}
	return cur, len(segs)
}

func (n *liveNode) children() []*db.Node {
	var out []*db.Node
	for _, k := range n.kids {
		out = append(out, &db.Node{ID: k.ID, Title: k.Title, URL: k.URL, Children: k.children()})
	}
	return out
}
