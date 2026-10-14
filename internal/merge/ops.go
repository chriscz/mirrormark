package merge

// Op is one change for the extension to make in P's browser. Parent is a
// browser id or the Ref of a node created earlier in the same round.
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
