package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

type Options struct {
	Paths   db.Paths
	Log     *slog.Logger
	Timeout time.Duration // how long the browser may take to apply a batch; default 10 s
	Poll    time.Duration // how often to look for changed files; default 1 s
}

// At most this many ops go in one apply message, and its ops take at most
// batchBytes when encoded, which leaves room for the rest of the message
// under Chrome's 1 MB limit.
const (
	batchOps   = 500
	batchBytes = maxOut - 1024
)

// errStop ends Run after telling the extension why.
var errStop = errors.New("host stopped")

type state struct {
	o        Options
	msgs     <-chan Msg
	out      io.Writer
	self     string
	unlock   func() // releases the profile lock; held while Run runs
	live     *round.Live
	fresh    bool      // live reflects the browser: no batch or edit since
	retry    bool      // the last round failed; run the next one at the poll
	failures int       // rounds failed in a row
	next     time.Time // the earliest retry of a failed round
	batch    int
	files    map[string]stamp
	problems []Problem      // what the current round warned about
	logged   map[string]int // warnings logged since the last round, with repeats
	quiet    map[string]int // warnings the last round logged, not to repeat
}

// stamp is what the poll compares: a file rewritten within the clock's
// granularity, or with its mtime restored, still changes size.
type stamp struct{ mtime, size int64 }

func stampOf(info os.FileInfo) stamp { return stamp{info.ModTime().UnixNano(), info.Size()} }

// failed is a round error with a code of its own; other round errors are
// apply-failed.
type failed struct {
	code string
	error
}

func (f failed) Unwrap() error { return f.error }

// retryDelay is how long to wait after the given number of failed rounds in
// a row. The first few retry at the next poll; then 5 s, then 30 s, so a
// browser that keeps refusing a change, or answering too slowly, does not
// take the global lock every second.
func retryDelay(failures int) time.Duration {
	switch {
	case failures < 3:
		return 0
	case failures < 6:
		return 5 * time.Second
	}
	return 30 * time.Second
}

// Run serves one extension connection until the browser closes it.
func Run(ctx context.Context, r io.Reader, w io.Writer, o Options) error {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Poll == 0 {
		o.Poll = time.Second
	}
	msgs := make(chan Msg)
	readErr := make(chan error, 1)
	go func() {
		defer close(msgs)
		for {
			m, err := ReadMsg(r)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	s := &state{o: o, msgs: msgs, out: w, files: map[string]stamp{}, logged: map[string]int{}, quiet: map[string]int{}}
	defer func() {
		// Twice: the second reports the warnings still repeating.
		s.flushWarnings()
		s.flushWarnings()
		if s.unlock != nil {
			s.unlock()
		}
	}()
	tick := time.NewTicker(o.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-msgs:
			if !ok {
				select {
				case err := <-readErr:
					if !errors.Is(err, io.EOF) {
						return err
					}
				default: // closed because ctx ended
				}
				return nil // the browser closed the port
			}
			if err := s.handle(m); err != nil {
				return err
			}
		case now := <-tick.C:
			if s.fresh && (s.retry && !now.Before(s.next) || s.filesChanged()) {
				s.run()
			}
		}
	}
}

func (s *state) send(m Msg) error {
	m.V = Version
	err := WriteMsg(s.out, m)
	if err != nil {
		s.logWarn("send failed", "type", m.Type, "err", err)
	}
	return err
}

// fail logs a problem, tells the extension, and stops the host.
func (s *state) fail(code, msg string) error {
	s.o.Log.Warn(msg, "code", code)
	s.send(Msg{Type: "error", Code: code, Message: msg})
	return fmt.Errorf("%w: %s", errStop, msg)
}

// warn logs a problem the host goes on after; the round's status message
// carries it to the extension's popup.
func (s *state) warn(code, msg string) {
	s.logWarn(msg, "code", code)
	s.problems = append(s.problems, Problem{Code: code, Message: msg})
}

// logWarn logs a warning unless the last round logged the same one. While a
// problem lasts every round repeats its warnings; the log gets each once,
// and how often it repeated when it stops.
func (s *state) logWarn(msg string, args ...any) {
	key := fmt.Sprint(append([]any{msg}, args...)...)
	if n, ok := s.quiet[key]; ok {
		s.logged[key] = n + 1
		return
	}
	s.logged[key] = 0
	s.o.Log.Warn(msg, args...)
}

// flushWarnings ends a round for logWarn.
func (s *state) flushWarnings() {
	for _, key := range slices.Sorted(maps.Keys(s.quiet)) {
		if _, still := s.logged[key]; !still && s.quiet[key] > 0 {
			s.o.Log.Info("warning stopped repeating", "warning", key, "repeats", s.quiet[key])
		}
	}
	s.quiet, s.logged = s.logged, map[string]int{}
}

func (s *state) handle(m Msg) error {
	if m.V != Version {
		return s.fail("unsupported-version", fmt.Sprintf("protocol version %d is not %d; restart Chrome after upgrading mirrormark", m.V, Version))
	}
	switch m.Type {
	case "hello":
		return s.hello(m)
	case "tree":
		if s.self == "" {
			s.o.Log.Debug("tree before hello ignored")
			return nil
		}
		live, err := round.Build(m.Nodes)
		if err != nil {
			s.logWarn("bad tree", "err", err)
			return nil
		}
		s.live, s.fresh = live, true
		// After a failed round this is most likely the tree that follows
		// the failed batch: running now would retry as fast as the browser
		// answers. The poll retries instead.
		if !s.retry {
			s.run()
		}
	case "changed":
		// The browser holds edits its next tree will bring. A round on the
		// tree before them would not see them, and could create again what
		// the user just made, such as a member folder; wait for that tree.
		s.fresh = false
	case "applied":
		s.o.Log.Info("late applied message ignored", "batch", m.Batch)
	case "error":
		s.logWarn("extension error", "code", m.Code, "message", m.Message)
	}
	return nil
}

func (s *state) hello(m Msg) error {
	if s.self != "" {
		return nil
	}
	if !db.ValidID.MatchString(m.DB) {
		return s.fail("bad-db", fmt.Sprintf("%q is not a database id", m.DB))
	}
	if _, err := os.Stat(s.o.Paths.ProfileFile(m.DB)); err != nil {
		return s.fail("db-missing", fmt.Sprintf("no database file for %s; run mirrormark install", m.DB))
	}
	// Held until Run returns: a copied profile bound to the same file must
	// not drive it twice.
	lock := s.o.Paths.ProfileLock(m.DB)
	unlock, err := db.TryLock(lock)
	if err != nil {
		if errors.Is(err, db.ErrLocked) {
			return s.fail("db-locked", fmt.Sprintf("another browser profile is using %s; was this profile copied?", m.DB))
		}
		return s.fail("db-lock-error", fmt.Sprintf("cannot lock %s: %v", m.DB, err))
	}
	s.unlock = unlock
	// install and the tests watch this mtime to see that the profile connected.
	now := time.Now()
	if err := os.Chtimes(lock, now, now); err != nil {
		s.logWarn("cannot touch lock file", "err", err)
	}
	s.self = m.DB
	s.o.Log.Info("connected", "db", m.DB, "browser", m.Browser)
	return nil
}

// filesChanged reports whether config.yaml or any database file changed
// since this host last looked. Polling a few files once a second costs
// nothing measurable and needs no inotify.
func (s *state) filesChanged() bool {
	return !maps.Equal(s.stat(), s.files)
}

func (s *state) stat() map[string]stamp {
	out := map[string]stamp{}
	names, _ := filepath.Glob(filepath.Join(s.o.Paths.ProfilesDir(), "*.yaml"))
	for _, n := range append(names, s.o.Paths.ConfigFile()) {
		if info, err := os.Stat(n); err == nil {
			out[n] = stampOf(info)
		}
	}
	return out
}

// run runs a round, then sends the extension a status message with every
// problem the round found; an empty one tells the popup all is well.
func (s *state) run() {
	s.problems = nil
	err := s.round()
	s.retry = err != nil
	if err != nil {
		code := "apply-failed"
		if f, ok := errors.AsType[failed](err); ok {
			code = f.code
		}
		s.warn(code, err.Error())
		s.failures++
		s.next = time.Now().Add(retryDelay(s.failures))
	} else {
		s.failures = 0
	}
	s.send(Msg{Type: "status", Problems: s.problems})
	s.flushWarnings()
}

// round runs one sync round under the global lock. It returns an error if
// the browser did not make every change, in which case it writes no file
// and the next round starts again from the same base, or if the round could
// not take the lock or write the file.
func (s *state) round() error {
	unlock, err := db.Lock(s.o.Paths.GlobalLock())
	if err != nil {
		return failed{"db-lock-error", fmt.Errorf("cannot take the lock that keeps rounds apart: %w; will retry", err)}
	}
	defer unlock()
	// Taken before the round reads the files, so that one changed meanwhile
	// starts another round.
	s.files = s.stat()

	cfg, err := db.LoadConfig(s.o.Paths.ConfigFile())
	if err != nil {
		s.warn("config-invalid", err.Error())
		return nil
	}
	self, err := db.LoadDB(s.o.Paths.ProfileFile(s.self))
	if err != nil {
		s.warn("db-missing", err.Error())
		return nil
	}
	others := map[string]*db.DB{}
	for _, members := range cfg.Unions {
		if _, mine := members[s.self]; !mine {
			continue
		}
		for id := range members {
			if id == s.self || others[id] != nil {
				continue
			}
			if d, err := db.LoadDB(s.o.Paths.ProfileFile(id)); err == nil {
				others[id] = d
			} else {
				s.logWarn("cannot read member", "id", id, "err", err)
			}
		}
	}
	plan := round.Make(round.Input{Config: cfg, Self: self, Others: others, Live: s.live, NewM: merge.NewM})
	for _, u := range slices.Sorted(maps.Keys(plan.Skipped)) {
		code := "union-skipped"
		if slices.Contains(plan.WrongTree, u) {
			code = "db-mismatch"
		}
		s.warn(code, fmt.Sprintf("not syncing %s: %s", u, plan.Skipped[u]))
	}
	created, err := s.apply(plan.Ops)
	if err != nil {
		return fmt.Errorf("%w; nothing written, will retry", err)
	}
	// An ensure-only plan sent only the folder creations and has nothing
	// to write; the tree that follows them starts the real round.
	if plan.EnsureOnly {
		return nil
	}
	for _, c := range plan.Conflicts {
		s.o.Log.Info("conflict", "folder", c.Folder, "ours", c.Ours, "theirs", c.Theirs)
	}
	next, changed, err := plan.Finish(created)
	if err != nil {
		s.logWarn("cannot finish round", "err", err)
		return nil
	}
	if !changed {
		return nil
	}
	if err := db.Backup(s.o.Paths, s.self, time.Now()); err != nil {
		s.logWarn("backup failed", "err", err)
	}
	// The browser already has this round's changes. The next round finds
	// them on both sides of the merge, so retrying it is safe.
	file := s.o.Paths.ProfileFile(s.self)
	if err := db.SaveDB(file, next); err != nil {
		return failed{"db-write-failed", fmt.Errorf("cannot write the database file: %w; will retry", err)}
	}
	if info, err := os.Stat(file); err == nil {
		s.files[file] = stampOf(info) // our own write is no news
	}
	return nil
}

// apply sends ops to the browser in batches and returns the browser ids of
// the nodes it created, by ref. It stops at the first batch that fails.
func (s *state) apply(ops []merge.Op) (map[string]string, error) {
	created := map[string]string{}
	if len(ops) == 0 {
		return created, nil
	}
	batches, err := split(ops)
	if err != nil {
		return nil, err
	}
	s.fresh = false // the tree is stale once we write to the browser
	for _, chunk := range batches {
		s.batch++
		if err := s.send(Msg{Type: "apply", Batch: s.batch, Ops: chunk}); err != nil {
			return nil, fmt.Errorf("cannot send changes to the browser: %w", err)
		}
		results, err := s.await(s.batch)
		if err == nil && len(results) != len(chunk) {
			err = fmt.Errorf("the browser answered %d of %d changes", len(results), len(chunk))
		}
		if err != nil {
			return nil, err
		}
		for i, r := range results {
			op := chunk[i]
			if !r.OK {
				// The browser stops at the first failure and answers the
				// rest "not run", so the first failure is the cause.
				s.logWarn("op failed", "op", op.Op, "id", op.ID, "ref", op.Ref, "err", r.Error)
				return nil, fmt.Errorf("the browser could not %s %s: %s", op.Op, describe(op), r.Error)
			}
			if op.Op == "create" {
				created[op.Ref] = r.ID
			}
		}
	}
	return created, nil
}

func describe(op merge.Op) string {
	switch {
	case op.Title != nil:
		return fmt.Sprintf("%q", *op.Title)
	case op.Expect != nil && op.Expect.Title != nil:
		return fmt.Sprintf("%q", *op.Expect.Title)
	}
	return "bookmark " + op.ID
}

// split cuts ops into batches that each fit one apply message.
func split(ops []merge.Op) ([][]merge.Op, error) {
	var out [][]merge.Op
	var cur []merge.Op
	size := 0
	for _, op := range ops {
		b, err := json.Marshal(op)
		if err != nil {
			return nil, err
		}
		n := len(b) + 1 // the comma
		if n > batchBytes {
			return nil, fmt.Errorf("a change to %s is too large to send to the browser", describe(op))
		}
		if len(cur) == batchOps || size+n > batchBytes {
			out, cur, size = append(out, cur), nil, 0
		}
		cur, size = append(cur, op), size+n
	}
	return append(out, cur), nil
}

// await waits for the extension's answer to one batch. Trees that arrive
// meanwhile are kept but not trusted until the post-batch tree comes.
func (s *state) await(batch int) ([]merge.OpResult, error) {
	t := time.NewTimer(s.o.Timeout)
	defer t.Stop()
	for {
		select {
		case m, ok := <-s.msgs:
			if !ok {
				return nil, io.EOF
			}
			switch m.Type {
			case "applied":
				if m.Batch == batch {
					return m.Results, nil
				}
			case "tree":
				if live, err := round.Build(m.Nodes); err == nil {
					s.live = live
				}
			case "error":
				s.logWarn("extension error", "code", m.Code, "message", m.Message)
			}
		case <-t.C:
			return nil, fmt.Errorf("the browser did not apply changes within %v", s.o.Timeout)
		}
	}
}
