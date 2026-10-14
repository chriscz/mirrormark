package round

import "github.com/chriscz/mirrormark/internal/merge"

// SetMerge replaces merge.Round for one test and returns the undo.
func SetMerge(f func(merge.Input) (merge.Result, error)) func() {
	old := mergeRound
	mergeRound = f
	return func() { mergeRound = old }
}
