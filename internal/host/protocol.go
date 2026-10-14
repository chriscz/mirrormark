// Package host is the native messaging host: Chrome starts it when the
// extension connects, and it runs sync rounds for that one profile.
//
// A round runs when the extension sends a tree (on connect, after a burst of
// bookmark events and after each batch) and when the poll sees that
// config.yaml or a database file changed. A round sends the browser its ops
// in batches and waits for each; if any op fails or the browser does not
// answer in time, it writes no file and the next round retries. A round
// that failed is retried at the next poll, not on the tree that follows the
// failed batch, so an op that always fails does not spin.
package host

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

// Version is the protocol version both sides must speak.
const Version = 1

// Msg is every message in either direction; Type says which fields count.
//
// Error codes the host sends: unsupported-version, bad-db, db-missing,
// db-locked (the host then exits); config-invalid, union-skipped,
// db-mismatch, apply-failed (the host goes on and retries).
type Msg struct {
	V       int              `json:"v"`
	Type    string           `json:"type"`
	DB      string           `json:"db,omitempty"`
	Browser string           `json:"browser,omitempty"`
	Nodes   []round.TreeNode `json:"nodes,omitempty"`
	Batch   int              `json:"batch,omitempty"`
	Ops     []merge.Op       `json:"ops,omitempty"`
	Results []merge.OpResult `json:"results,omitempty"`
	Code    string           `json:"code,omitempty"`
	Message string           `json:"message,omitempty"`
}

// Chrome limits messages to the extension to 1 MB. Messages from it may be
// larger; 64 MB bounds what a broken peer can make us allocate.
const (
	maxOut = 1 << 20
	maxIn  = 64 << 20
)

func ReadMsg(r io.Reader) (Msg, error) {
	var n uint32
	if err := binary.Read(r, binary.NativeEndian, &n); err != nil {
		return Msg{}, err
	}
	if n > maxIn {
		return Msg{}, fmt.Errorf("message of %d bytes is too large", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Msg{}, err
	}
	var m Msg
	if err := json.Unmarshal(buf, &m); err != nil {
		return Msg{}, fmt.Errorf("bad message: %w", err)
	}
	return m, nil
}

func WriteMsg(w io.Writer, m Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > maxOut {
		return fmt.Errorf("message of %d bytes is over Chrome's 1 MB limit", len(b))
	}
	// One write, so a reader never sees a length without its message.
	out := binary.NativeEndian.AppendUint32(make([]byte, 0, 4+len(b)), uint32(len(b)))
	_, err = w.Write(append(out, b...))
	return err
}
