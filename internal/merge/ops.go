package merge

// Op is one change for the extension to make in P's browser. Parent is a
// browser id or the Ref of a node created earlier in the same round.
//
// The extension runs the ops in order, one at a time, and stops at the first
// that fails, answering the rest "not run": each op assumes the ones before
// it landed. The host then writes no file, and the next round retries.
//
// A remove takes one bookmark or one empty folder: the extension must use
// bookmarks.remove, never removeTree. The ops remove a folder's contents
// before the folder, so a folder that is not empty still holds something
// the round means to keep, and the remove must fail.
type Op struct {
	Op     string  `json:"op"` // create, update, move, remove
	ID     string  `json:"id,omitempty"`
	Ref    string  `json:"ref,omitempty"`
	Parent string  `json:"parent,omitempty"`
	Index  *int    `json:"index,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
	Expect *Expect `json:"expect,omitempty"`
}

// Expect is what the op expects to find. The extension fails the op as
// "stale" if the node differs, so an edit made during a batch survives.
type Expect struct {
	Parent string  `json:"parent,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
}

// OpResult is the extension's answer for one op; ID is set for create.
type OpResult struct {
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}
