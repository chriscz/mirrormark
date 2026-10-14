package merge_test

import (
	"errors"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
)

// TestFuzzUnion runs three members through fakebrowser.Fuzz with no limit
// on removes, so some seeds rightly stop at the wrong-tree guard. Run more
// seeds with -mirrormark.fuzz=20000.
func TestFuzzUnion(t *testing.T) {
	seeds := fakebrowser.Seeds()
	guarded, failed := 0, 0
	for seed := range seeds {
		err := fakebrowser.Fuzz(uint64(seed), newSim("p", "q", "r"), -1)
		switch {
		case errors.Is(err, merge.ErrWrongTree):
			guarded++
		case err != nil:
			t.Errorf("seed %d: %v", seed, err)
			if failed++; failed > 10 {
				t.Fatal("too many failing seeds")
			}
		}
	}
	t.Logf("%d seeds, %d stopped by the wrong-tree guard", seeds, guarded)
}

func (s *sim) Len() int { return len(s.ms) }

func (s *sim) Member(i int) (*fakebrowser.Browser, string, []*db.Node) {
	return s.ms[i].b, s.ms[i].root, s.ms[i].rec
}

func (s *sim) Edited(i int) { s.ms[i].edited = true }

func (s *sim) Round(i int, fail func(int) bool) (bool, error) {
	s.fail = fail
	return s.round(s.ms[i])
}
