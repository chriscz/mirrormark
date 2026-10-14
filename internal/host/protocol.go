// Package host is the native messaging host: Chrome starts it when the
// extension connects, and it runs sync rounds for that one profile.
//
// A round runs when the extension sends a tree (on connect, after a burst of
// bookmark events and after each batch) and when the poll sees that
// config.yaml or a database file changed. The extension sends "changed" at
// the first bookmark event of a burst, before it waits for quiet to send the
// tree; until that tree comes the poll starts no round, since one on the old
// tree could create again what the user just made. A round sends the browser its ops
// in batches and waits for each; if any op fails or the browser does not
// answer in time, it writes no file and the next round retries. A round
// that failed is retried at the poll, not on the tree that follows the
// failed batch and only after retryDelay, so an op that always fails does
// not spin (see Msg).
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
// The host sends "error" only when it stops, with one of these codes:
// unsupported-version, bad-db, db-missing (no database file), db-locked
// (another host uses the database), db-lock-error (the lock file cannot be
// locked).
//
// After every round the host sends "status" with the problems the round
// found, none when it synced cleanly, so the popup shows only what is still
// wrong. Their codes: config-invalid, db-missing (the database file cannot
// be read), union-skipped, db-mismatch, apply-failed (the browser did not
// make every change), db-write-failed (the browser made them but the
// database file could not be written), db-lock-error (the lock that keeps
// rounds apart cannot be taken). A round with apply-failed, db-write-failed
// or db-lock-error is retried every 5 s until one succeeds, or sooner when a
// file changes.
type Msg struct {
	V        int              `json:"v"`
	Type     string           `json:"type"`
	DB       string           `json:"db,omitempty"`
	Browser  string           `json:"browser,omitempty"`
	Nodes    []round.TreeNode `json:"nodes,omitempty"`
	Batch    int              `json:"batch,omitempty"`
	Ops      []merge.Op       `json:"ops,omitempty"`
	Results  []merge.OpResult `json:"results,omitempty"`
	Code     string           `json:"code,omitempty"`
	Message  string           `json:"message,omitempty"`
	Problems []Problem        `json:"problems,omitempty"`
}

// Problem is one thing a round could not do.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
