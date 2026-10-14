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
	o      Options
	msgs   <-chan Msg
	out    io.Writer
	self   string
	unlock func() // releases the profile lock; held while Run runs
	live   *round.Live
	fresh  bool // live reflects the browser after our last batch
	retry  bool // the last round failed; run the next one at the poll
	batch  int
	mtimes map[string]time.Time
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
	s := &state{o: o, msgs: msgs, out: w, mtimes: map[string]time.Time{}}
	defer func() {
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
		case <-tick.C:
			if s.fresh && (s.retry || s.filesChanged()) {
				s.run()
			}
		}
	}
}

func (s *state) send(m Msg) error {
	m.V = Version
	err := WriteMsg(s.out, m)
	if err != nil {
		s.o.Log.Warn("send failed", "type", m.Type, "err", err)
	}
	return err
}

func (s *state) fail(code, msg string) error {
	s.warn(code, msg)
	return fmt.Errorf("%w: %s", errStop, msg)
}

// warn logs a problem and shows it in the extension's popup.
func (s *state) warn(code, msg string) {
	s.o.Log.Warn(msg, "code", code)
	s.send(Msg{Type: "error", Code: code, Message: msg})
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
			return nil
		}
		live, err := round.Build(m.Nodes)
		if err != nil {
			s.o.Log.Warn("bad tree", "err", err)
			return nil
		}
		s.live, s.fresh = live, true
		// After a failed round this is most likely the tree that follows
		// the failed batch: running now would retry as fast as the browser
		// answers. The poll retries instead.
		if !s.retry {
			s.run()
		}
	case "applied":
		s.o.Log.Info("late applied message ignored", "batch", m.Batch)
	case "error":
		s.o.Log.Warn("extension error", "code", m.Code, "message", m.Message)
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
		return s.fail("db-locked", err.Error())
	}
	s.unlock = unlock
	// install and the tests watch this mtime to see that the profile connected.
	now := time.Now()
	if err := os.Chtimes(lock, now, now); err != nil {
		s.o.Log.Warn("cannot touch lock file", "err", err)
	}
	s.self = m.DB
	s.o.Log.Info("connected", "db", m.DB, "browser", m.Browser)
	return nil
}

// filesChanged reports whether config.yaml or any database file changed
// since this host last looked. Polling a few files once a second costs
// nothing measurable and needs no inotify.
func (s *state) filesChanged() bool {
	return !maps.Equal(s.stat(), s.mtimes)
}

func (s *state) stat() map[string]time.Time {
	out := map[string]time.Time{}
	names, _ := filepath.Glob(filepath.Join(s.o.Paths.ProfilesDir(), "*.yaml"))
	for _, n := range append(names, s.o.Paths.ConfigFile()) {
		if info, err := os.Stat(n); err == nil {
			out[n] = info.ModTime()
		}
	}
	return out
}

func (s *state) run() {
	err := s.round()
	s.retry = err != nil
	if err != nil {
		s.warn("apply-failed", err.Error())
	}
}

// round runs one sync round under the global lock. It returns an error if
// the browser did not make every change; it then writes no file, so the
// next round starts again from the same base.
func (s *state) round() error {
	unlock, err := db.Lock(s.o.Paths.GlobalLock())
	if err != nil {
		s.o.Log.Warn("cannot lock", "err", err)
		return nil // nothing was sent; the next tree or file change retries
	}
	defer unlock()
	defer func() { s.mtimes = s.stat() }() // under the lock; covers our own write

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
				s.o.Log.Warn("cannot read member", "id", id, "err", err)
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
	// An ensure-only plan sent only the folder creations; its conflicts
	// belong to ops the next round will make again.
	if plan.EnsureOnly {
		return nil
	}
	for _, c := range plan.Conflicts {
		s.o.Log.Info("conflict", "folder", c.Folder, "ours", c.Ours, "theirs", c.Theirs)
	}
	next, changed, err := plan.Finish(created)
	if err != nil {
		s.o.Log.Warn("cannot finish round", "err", err)
		return nil
	}
	if !changed {
		return nil
	}
	if err := db.Backup(s.o.Paths, s.self, time.Now()); err != nil {
		s.o.Log.Warn("backup failed", "err", err)
	}
	if err := db.SaveDB(s.o.Paths.ProfileFile(s.self), next); err != nil {
		s.o.Log.Warn("cannot write database", "err", err)
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
				s.o.Log.Warn("op failed", "op", op.Op, "id", op.ID, "ref", op.Ref, "err", r.Error)
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
				s.o.Log.Warn("extension error", "code", m.Code, "message", m.Message)
			}
		case <-t.C:
			return nil, fmt.Errorf("the browser did not apply changes within %v", s.o.Timeout)
		}
	}
}
