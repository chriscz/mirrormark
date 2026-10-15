package host

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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
	Ping    time.Duration // Serve: how often to ping each connection; default 20 s
	Origin  string        // the connection's Origin; Serve sets it, and then the hello starts a challenge
	Port    string        // the port the server listens on, named in the host's proof; Serve sets it
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
	conn     Conn
	self     string
	pending  pending
	unlock   func() // releases the profile lock; held while Run runs
	live     *round.Live
	fresh    bool      // live reflects the browser: no batch or edit since
	retry    bool      // the last round failed; the poll retries it after retryDelay
	next     time.Time // the earliest retry of a failed round
	batch    int
	files    map[string]stamp
	problems []Problem // what the current round warned about
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

// retryDelay is how long a failed round waits before the poll retries it,
// so a browser that keeps refusing a change, or answering too slowly, does
// not take the global lock, or add to the log, every second. A changed file
// starts a round sooner.
const retryDelay = 5 * time.Second

// Run serves one extension connection until the browser closes it.
func Run(ctx context.Context, c Conn, o Options) error {
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
			m, err := c.Read()
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
	s := &state{o: o, msgs: msgs, conn: c, files: map[string]stamp{}}
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
		case now := <-tick.C:
			if s.fresh && (s.retry && !now.Before(s.next) || s.filesChanged()) {
				s.run()
			}
		}
	}
}

func (s *state) send(m Msg) error {
	m.V = Version
	err := s.conn.Write(m)
	if err != nil {
		s.o.Log.Warn("send failed", "type", m.Type, "err", err)
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
	s.o.Log.Warn(msg, "code", code)
	s.problems = append(s.problems, Problem{Code: code, Message: msg})
}

func (s *state) handle(m Msg) error {
	if m.V != Version {
		return s.fail("unsupported-version", fmt.Sprintf("protocol version %d is not %d; restart the browser after upgrading mirrormark", m.V, Version))
	}
	switch m.Type {
	case "hello":
		return s.hello(m)
	case "proof":
		return s.proof(m)
	case "tree":
		if s.self == "" {
			s.o.Log.Debug("tree before hello ignored")
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
	case "changed":
		// The browser holds edits its next tree will bring. A round on the
		// tree before them would not see them, and could create again what
		// the user just made, such as a member folder; wait for that tree.
		s.fresh = false
	case "applied":
		s.o.Log.Info("late applied message ignored", "batch", m.Batch)
	case "error":
		s.o.Log.Warn("extension error", "code", m.Code, "message", m.Message)
	}
	return nil
}

func (s *state) hello(m Msg) error {
	if s.self != "" || s.pending.db != "" {
		return nil
	}
	if !db.ValidID.MatchString(m.DB) {
		return s.fail("bad-db", fmt.Sprintf("%q is not a database id", m.DB))
	}
	if _, err := os.Stat(s.o.Paths.ProfileFile(m.DB)); err != nil {
		return s.fail("db-missing", fmt.Sprintf("no database file for %s; run mirrormark install", m.DB))
	}
	// Through the server, a connection must prove which database it is
	// before the host runs: native messaging needed no proof, since the
	// browser started the host for its own extension.
	if s.o.Origin != "" {
		return s.challenge(m)
	}
	return s.accept(m.DB, m.Browser)
}

// accept runs the host for database id from now on.
func (s *state) accept(id, browser string) error {
	// Held until Run returns: a copied profile bound to the same file must
	// not drive it twice.
	lock := s.o.Paths.ProfileLock(id)
	unlock, err := db.TryLock(lock)
	if err != nil {
		if errors.Is(err, db.ErrLocked) {
			return s.fail("db-locked", fmt.Sprintf("another browser profile is using %s; was this profile copied?", id))
		}
		return s.fail("db-lock-error", fmt.Sprintf("cannot lock %s: %v", id, err))
	}
	s.unlock = unlock
	// install and the tests watch this mtime to see that the profile connected.
	now := time.Now()
	if err := os.Chtimes(lock, now, now); err != nil {
		s.o.Log.Warn("cannot touch lock file", "err", err)
	}
	s.self = id
	// A connection that limits how long an unproven peer may take learns
	// that this one is through.
	if c, ok := s.conn.(interface{ proven() }); ok {
		c.proven()
	}
	s.o.Log.Info("connected", "db", id, "browser", browser)
	return nil
}

// pending is a hello waiting for the extension's proof.
type pending struct{ db, browser, token, nonce string }

// nonceRe is a nonce as both sides write it: 32 random bytes in hex.
var nonceRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// challenge answers a hello through the server. The connection came from
// the database's origin, but any program can send any Origin, and in
// Chromium every profile has the same one. So each side proves it holds
// the token install handed this profile, without sending it: the host
// first, so the extension tells a program posing as mirrormark nothing.
// The port in the host's proof stops a program on another port relaying
// the hello here and passing back this host's proof.
func (s *state) challenge(m Msg) error {
	d, err := db.LoadDB(s.o.Paths.ProfileFile(m.DB))
	if err != nil {
		return s.fail("db-missing", fmt.Sprintf("cannot read the database file for %s: %v", m.DB, err))
	}
	if d.Profile.Token == "" || d.Profile.Origin != s.o.Origin || !nonceRe.MatchString(m.Nonce) {
		return s.fail("bad-token", fmt.Sprintf("this profile's binding to %s is out of date; run mirrormark install for this profile again", m.DB))
	}
	b := make([]byte, 32)
	rand.Read(b) // never fails
	s.pending = pending{db: m.DB, browser: m.Browser, token: d.Profile.Token, nonce: hex.EncodeToString(b)}
	return s.send(Msg{Type: "challenge", Nonce: s.pending.nonce, Proof: mac(d.Profile.Token, "server:"+s.o.Port+":"+m.Nonce)})
}

// proof checks the extension's answer to the challenge, and only then
// runs the host for its database.
func (s *state) proof(m Msg) error {
	p := s.pending
	if s.self != "" || p.db == "" {
		return nil // no challenge is waiting
	}
	if !hmac.Equal([]byte(m.Proof), []byte(mac(p.token, "extension:"+p.nonce))) {
		return s.fail("bad-token", fmt.Sprintf("this profile's binding to %s is out of date; run mirrormark install for this profile again", p.db))
	}
	return s.accept(p.db, p.browser)
}

// mac is HMAC-SHA256 of msg, keyed by the token as text, in lowercase hex.
func mac(token, msg string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
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
		s.next = time.Now().Add(retryDelay)
	}
	s.send(Msg{Type: "status", Problems: s.problems})
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
		s.o.Log.Warn("cannot finish round", "err", err)
		return nil
	}
	if !changed {
		return nil
	}
	if err := db.Backup(s.o.Paths, s.self, time.Now()); err != nil {
		s.o.Log.Warn("backup failed", "err", err)
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
