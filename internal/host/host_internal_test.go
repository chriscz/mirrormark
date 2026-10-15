package host

import (
	"io"
	"log/slog"
	"testing"
)

// Before the host runs for a database, nothing but a hello or a proof may
// change its state: a "changed" from nobody must not hold back the round
// on the tree after the proof.
func TestMessagesBeforeTheHostRunsChangeNothing(t *testing.T) {
	s := &state{o: Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, fresh: true}
	for _, typ := range []string{"changed", "applied", "error", "tree", "status"} {
		if err := s.handle(Msg{V: Version, Type: typ}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if !s.fresh || s.live != nil || s.self != "" {
			t.Fatalf("%s changed the state: fresh %v, live %v", typ, s.fresh, s.live)
		}
	}
}
