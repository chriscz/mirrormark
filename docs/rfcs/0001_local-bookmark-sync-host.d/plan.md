# mirrormark for Google Chrome on Linux: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. When writing Go, use the `use-modern-go` skill.

**Goal:** One Go binary, `mirrormark`, that installs an embedded Manifest V3 extension into Google Chrome on Linux, binds chosen profiles to per-profile YAML database files, and keeps union folders the same across those profiles within about 3 seconds.

**Architecture:** Chrome starts `mirrormark` as a native messaging host, one process per bound profile. Each host runs rounds under a global `flock`: it reads `config.yaml` and the member database files, runs a pure three-way merge per union (`merge.Round`), sends the resulting ops to its own browser, and writes only its own database file. Hosts notice each other's writes by polling file modification times once a second. `mirrormark install` packs the embedded extension as a CRX3, writes Chrome's external-extension JSON and the system native-host manifest (as root, via `sudo install` if needed), creates the database files and binds each profile by opening `chrome-extension://<id>/bind.html#<db>` in it.

**Tech Stack:** Go 1.27.2 (standard library plus `go.yaml.in/yaml/v3` v3.0.5); plain JavaScript MV3 extension, no build step; Docker dev image on `ubuntu:24.04` with Google Chrome stable 155.0.8059.79 and Xvfb; end-to-end tests in Go over Chrome's `--remote-debugging-pipe`.

**Spec:** [`../0001_local-bookmark-sync-host.md`](../0001_local-bookmark-sync-host.md) (the RFC). Empirical Chrome findings: [`chrome-install-spike.md`](chrome-install-spike.md).

## Global Constraints

- Scope: Google Chrome on Linux only. No Firefox, Chromium, Brave, Windows or macOS code paths.
- Module `github.com/chriscz/mirrormark`; `go 1.27` in `go.mod`. The only third-party dependency is `go.yaml.in/yaml/v3 v3.0.5`. The e2e driver uses only the standard library.
- Pinned toolchain in the `Dockerfile`: `ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55`; Go `1.27.2`, sha256 `ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5`; Google Chrome `155.0.8059.79-1`, sha256 `b7726070ca707965e9a17b38e4f15ba283bf6f65c339626c092862af7b19badc`.
- Nothing is installed on the host machine. Every build and test runs as `bin/dev run make …`. The `Makefile` is the one build system; no other build scripts.
- No `any`/`interface{}` values and no unchecked type assertions. `any` as a type-parameter constraint is fine.
- Never reference the RFC in code, comments, tests or build files (no RFC numbers, section numbers or `docs/rfcs/` links). Comments say why in their own words.
- Every commit: Conventional Commits with scope `rfc-0001`; new functionality ends its subject with `[new feature]`; body ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Each commit builds and passes `bin/dev run make` on its own.
- Each task updates `README.md` for what it adds, in the same commit. How the code works lives in package doc comments; an as-built document can be generated from the code later instead of being kept by hand.
- Native host name: `com.github.chriscz.mirrormark`. Extension permissions: `bookmarks`, `nativeMessaging`, `storage`; no host permissions; no web-accessible resources.
- Paths (XDG, defaults shown): config `~/.config/mirrormark/config.yaml`; data `~/.local/share/mirrormark/` (`profiles/<id>.yaml`, `profiles/<id>.lock`, `backups/<id>/<YYYY-MM-DD>.yaml`, `lock`, `extension/`, `extension.pem`, `mirrormark.crx`); state `~/.local/state/mirrormark/` (`log`). The host touches `profiles/<id>.lock` on each `hello`; its mtime is how `install` and the e2e tests see that a profile connected. YAML and key files mode `0600`, directories `0700`, CRX `0644`.
- Timings: extension sends a tree after 0.5 s without bookmark events; host polls files every 1 s; host waits 10 s for a batch to apply; at most 500 ops per `apply` message (messages to the extension must stay under 1 MB).
- Wrong-tree guard: a round in which more than half of the base nodes are gone from ours (purges included) or are live in base but gone from theirs, when base holds more than 5 nodes, writes nothing and reports `db-mismatch`.
- Backups: before the first write of each day; keep the newest 30 per profile.
- Default ids and labels: database id `chrome-<slug of profile name>`, label `chrome/<slug>`, with `-2`, `-3`… appended when taken. Slug: lower case, runs of characters other than `a-z0-9` become `-`, trimmed.
- URLs match exactly (no normalisation). Paths resolve in Chrome's account folders when they exist (`syncing: true`), else the local ones. A folder conflict lets theirs win and logs it.

## Verified before planning

Run in Docker on 2026-10-14 and recorded in `chrome-install-spike.md`:

- `google-chrome --profile-directory=X chrome-extension://<id>/bind.html#<db>` opens the page in profile X, with Chrome running or not, once the extension is loaded in X. On X's first start after the external JSON appears, the first attempt fails and a retry 4 s later works. Install therefore confirms each binding through the mtime of the profile's lock file, which the host touches on `hello`, and retries up to 3 times. The popup fallback is not needed.
- CDP over `--remote-debugging-pipe` works in branded Chrome 155 under Xvfb, and a second `google-chrome --user-data-dir=<same>` invocation still forwards its URL to the running instance.

## File structure

```
Dockerfile                  dev image: Go, later Chrome + Xvfb (task 8)
Makefile                    the one build system
bin/dev                     runs commands in the dev image
README.md                   users: install, usage, config, development
go.mod, go.sum
cmd/mirrormark/
  main.go                   subcommand dispatch, host-mode detection
  install.go                `install`, `uninstall`, `unpack`
  main_test.go, install_test.go
internal/db/                YAML files, paths, locks, backups
  paths.go types.go file.go config.go lock.go (+ _test.go)
internal/merge/             pure three-way merge of one union
  merge.go ops.go (+ merge_test.go)
internal/fakebrowser/       in-memory bookmarks model that applies ops like the extension (tests only)
  fakebrowser.go
internal/round/             one round across all unions of one profile, still pure
  live.go round.go (+ round_test.go, property_test.go)
internal/host/              native messaging framing, protocol, host loop
  protocol.go host.go (+ host_test.go)
internal/chrome/            Chrome profiles, key, CRX3, system files, binding
  profiles.go key.go crx.go files.go bind.go (+ _test.go)
extension/                  the embedded extension (go:embed) and Unpack
  embed.go manifest.json background.js bind.html bind.js popup.html popup.js embed_test.go
e2e/                        //go:build e2e; two Chrome instances over CDP pipes
  cdp.go browser.go env.go install_test.go sync_test.go
```

## Interfaces between tasks

These names and types are fixed. Later tasks use them exactly.

```go
// internal/db (task 2)
type Paths struct{ Config, Data, State string }
func DefaultPaths() (Paths, error)
func (p Paths) ConfigFile() string        // <Config>/config.yaml
func (p Paths) ProfilesDir() string       // <Data>/profiles
func (p Paths) ProfileFile(id string) string
func (p Paths) ProfileLock(id string) string
func (p Paths) GlobalLock() string        // <Data>/lock
func (p Paths) BackupDir(id string) string
func (p Paths) LogFile() string
type Node struct{ Title, URL, ID, M, By, Suffix string; Children []*Node }   // yaml: title,url,id,m,by,suffix,children
type Member struct{ Seq int64; Path, ID string; Children []*Node }          // yaml: seq,path,id,children
type Profile struct{ ID, Label, Browser, Path string }
type DB struct{ Format int; Profile Profile; Unions map[string]*Member }
type Config struct{ Format int; Unions map[string]map[string]string }
var ValidID *regexp.Regexp                // ^[a-z0-9][a-z0-9-]*$
func SplitPath(p string) ([]string, error)
func WriteAtomic(path string, data []byte, mode os.FileMode) error
func LoadDB(path string) (*DB, error)
func SaveDB(path string, d *DB) error
func LoadAll(p Paths) (map[string]*DB, map[string]error)
func LoadConfig(path string) (*Config, error)   // missing file → empty valid config
func (c *Config) Validate() error
func EqualNodes(a, b []*Node) bool
func Backup(p Paths, id string, now time.Time) error
func Lock(path string) (unlock func(), err error)      // blocking
func TryLock(path string) (unlock func(), err error)   // ErrLocked if held
var ErrLocked error
func Connected(p Paths, id string, since time.Time) bool   // a host touched profiles/<id>.lock after since

// internal/merge (task 3)
const DeletedTitle = "_deleted"
var ErrWrongTree error  // "db-mismatch: … would move to _deleted or be purged; is a database file truncated, or this profile bound to the wrong one?"
type Input struct{ Base []*db.Node; HasBase bool; Ours, Theirs []*db.Node; RootID, Label string; NewM func() string }
type Conflict struct{ Folder bool; Ours, Theirs string }
type Result struct{ Tree []*db.Node; Ops []Op; Conflicts []Conflict }
type Op struct{ Op, ID, Ref, Parent string; Index *int; Title, URL *string; Expect *Expect }  // json tags below
type Expect struct{ Parent string; Title, URL *string }
type OpResult struct{ OK bool; ID, Error string }
func Round(in Input) (Result, error)
func NewM() string

// internal/fakebrowser (task 3, Tree added in task 4)
func New() *Browser   // ids "1" Bookmarks bar, "2" Other bookmarks, "3" Mobile bookmarks
func (b *Browser) Add(parentID, title, url string) string   // url "" makes a folder
func (b *Browser) Rename(id, title string); SetURL(id, url string); Move(id, parentID string); Remove(id string); RemoveTree(id string)
func (b *Browser) Find(parentID, title string) string
func (b *Browser) Children(id string) []*db.Node
func (b *Browser) Lines(id string) []string
func (b *Browser) Apply(ops []merge.Op, fail func(i int) bool) []merge.OpResult
func (b *Browser) Tree() []round.TreeNode          // task 4
func NodeLines(ns []*db.Node) []string
type Union interface{ Len() int; Member(i int) (*Browser, string, []*db.Node); Edited(i int); Round(i int, fail func(int) bool) (bool, error) } // task 4
func Fuzz(seed uint64, u Union, budget int) error  // random edits and failing rounds; budget caps removes per burst, -1 none
func Seeds() int                                   // -mirrormark.fuzz, default 300, 30 with -short

// internal/round (task 4)
type TreeNode struct{ ID string; Parent *string; Index int; Title, URL, Root string; Syncing bool } // json: id,parent,index,title,url,root,syncing
type Live struct{ /* unexported */ }
func Build(nodes []TreeNode) (*Live, error)
type Input struct{ Config *db.Config; Self *db.DB; Others map[string]*db.DB; Live *Live; NewM func() string }
type Plan struct{ Ops []merge.Op; EnsureOnly bool; Conflicts []merge.Conflict; Skipped map[string]string; WrongTree []string /* + unexported */ }
func Make(in Input) Plan
func (p Plan) Finish(created map[string]string) (*db.DB, bool, error)

// internal/host (task 5)
type Msg struct{ V int; Type, DB, Browser string; Nodes []round.TreeNode; Batch int; Ops []merge.Op; Results []merge.OpResult; Code, Message string }
func ReadMsg(r io.Reader) (Msg, error)
func WriteMsg(w io.Writer, m Msg) error
type Options struct{ Paths db.Paths; Log *slog.Logger; Timeout, Poll time.Duration }
func Run(ctx context.Context, r io.Reader, w io.Writer, o Options) error

// extension (task 6)
const HostName = "com.github.chriscz.mirrormark"
var Files embed.FS
func Unpack(dir string, publicKeyDER []byte) error
func Version() (string, error)   // for external_version

// internal/chrome (task 7)
const ExternalDir = "/usr/share/google-chrome/extensions"
const SystemHostDir = "/etc/opt/chrome/native-messaging-hosts"
type Profile struct{ Dir, Name string }
func DefaultUserDataDir() (string, error)
func Profiles(userDataDir string) ([]Profile, error)
func LoadKey(path string) (*rsa.PrivateKey, error)
func LoadOrCreateKey(path string) (*rsa.PrivateKey, error)
func PublicKeyDER(k *rsa.PrivateKey) []byte
func ExtensionID(publicKeyDER []byte) string
func WriteCRX(dir string, key *rsa.PrivateKey, out string) error
type Extension struct{ ID, Version, Dir, CRX string }
func Prepare(dataDir string) (Extension, error)
type File struct{ Path string; Data []byte }
func SystemFilePaths(id string) []string
func SystemFiles(e Extension, bin string) []File
func FilesCurrent(fs []File) bool
func WriteFiles(fs []File) error
func RemoveFiles(paths []string) error
var BindWait time.Duration // 4 s; tests shorten it
func Bind(chromeBin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error
```

## Tasks

1. Dev image, Makefile, `bin/dev` and Go skeleton.
2. YAML database and config files, atomic writes, locks, backups.
3. Merge engine (`merge.Round`) with the fake browser, test first.
4. Round planning across member files, with convergence property tests.
5. Native messaging host.
6. Embedded extension.
7. `install`, `uninstall` and `unpack` for Google Chrome.
8. E2E harness (Chrome in the image, Go CDP over a pipe) and the install-and-bind test.
9. E2E union sync tests: edits (add, rename, move) with latency, delete and purge, conflict, first sync, restart catch-up.

Each task is one commit.

---

### Task 1: Dev image, Makefile, `bin/dev` and Go skeleton

**Files:**
- Create: `Dockerfile`, `bin/dev`, `Makefile`, `.gitignore`, `go.mod`, `cmd/mirrormark/main.go`, `cmd/mirrormark/main_test.go`, `README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `bin/dev run make` (targets `all` = `check build`, `check` = `check-fmt check-vet check-tests`, `build` → `build/mirrormark`); `run(args []string, stdin io.Reader, stdout, stderr io.Writer) int` in `cmd/mirrormark/main.go`, which later tasks extend with subcommands.

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# Dev image. Everything builds and tests in here: bin/dev run make
FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git make \
 && rm -rf /var/lib/apt/lists/*

ARG GO_VERSION=1.27.2
ARG GO_SHA256=ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5
RUN curl -fsSLo /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
 && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
 && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH=/usr/local/go/bin:$PATH

# The source is bind-mounted at runtime; nothing is copied in.
WORKDIR /src
RUN git config --system --add safe.directory /src
```

- [ ] **Step 2: Write `bin/dev`** (mode 755)

```bash
#!/usr/bin/env bash
# Run mirrormark's build tools inside the dev Docker image (see Dockerfile).
set -euo pipefail

IMAGE=mirrormark-dev
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
    cat <<EOF
Usage: bin/dev <command> [args...]

Commands:
  image build   Build the image, or rebuild it after a Dockerfile change
  shell         Open a temporary shell in the dev container
  run <cmd...>  Run one command in the dev container
  help          Show this help

The repo is mounted at /src and commands run as your user. Go's caches live
in .cache/ in the repo so they survive between containers.

Examples:
  bin/dev run make
  bin/dev run make check-e2e
EOF
}

image_build() { docker build -t "$IMAGE" "$ROOT"; }

container() {
    docker image inspect "$IMAGE" >/dev/null 2>&1 || image_build
    local tty=()
    [ -t 0 ] && tty=(-t)
    # HOME=/tmp: your UID has no home directory in the image.
    docker run --rm -i "${tty[@]}" -u "$(id -u):$(id -g)" -e HOME=/tmp \
        -e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/mod -e GOFLAGS=-modcacherw \
        -v "$ROOT":/src "$IMAGE" "$@"
}

case "${1:-help}" in
    image) [ "${2:-}" = build ] || { usage >&2; exit 1; }; image_build ;;
    shell) container bash ;;
    run) shift; [ $# -gt 0 ] || { usage >&2; exit 1; }; container "$@" ;;
    help | -h | --help) usage ;;
    *) usage >&2; exit 1 ;;
esac
```

- [ ] **Step 3: Write the Makefile and `.gitignore`**

`Makefile`:

```make
# The one build system. Run targets in the dev image: bin/dev run make <target>
GO  ?= go
BIN := build/mirrormark

all: check build
.PHONY: all

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/mirrormark
.PHONY: build

check: check-fmt check-vet check-tests
.PHONY: check

check-fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt -w needed:"; echo "$$out"; exit 1; fi
.PHONY: check-fmt

check-vet:
	$(GO) vet ./...
.PHONY: check-vet

check-tests:
	$(GO) test -count=1 ./...
.PHONY: check-tests

clean:
	rm -rf build
.PHONY: clean
```

`.gitignore`:

```
/build/
/.cache/
```

- [ ] **Step 4: Write the failing test** `cmd/mirrormark/main_test.go`

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutArgsPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "Usage: mirrormark") {
		t.Fatalf("stderr %q has no usage", errb.String())
	}
}

func TestHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: mirrormark") {
		t.Fatalf("stdout %q has no usage", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}
```

`go.mod`:

```
module github.com/chriscz/mirrormark

go 1.27
```

- [ ] **Step 5: Run it to see it fail**

Run: `bin/dev image build && bin/dev run go test ./cmd/mirrormark/`
Expected: FAIL, `undefined: run`.

- [ ] **Step 6: Write `cmd/mirrormark/main.go`**

```go
// Command mirrormark keeps bookmark folders the same across browser profiles
// through YAML files on this computer.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usageText = `Usage: mirrormark <command> [options]

Commands:
  help        Show this help
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "mirrormark: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}
```

- [ ] **Step 7: Write the README**

`README.md`:

```markdown
# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No server, no account, no network.

Status: in development. Google Chrome on Linux comes first.

## Development

Everything builds and tests in a Docker image; nothing is installed on your
machine except Docker.

    bin/dev run make          # format check, vet, unit tests, build
    bin/dev shell             # a shell in the dev container

The binary lands in `build/mirrormark`.

## Licence

MPL-2.0. See `LICENSE`.
```

- [ ] **Step 8: Verify**

Run: `bin/dev run make`
Expected: gofmt prints nothing, vet passes, `ok github.com/chriscz/mirrormark/cmd/mirrormark`, and `build/mirrormark` exists. Then `bin/dev run build/mirrormark help` prints the usage.

- [ ] **Step 9: Commit**

```bash
git add Dockerfile bin/dev Makefile .gitignore go.mod cmd README.md
git commit -m "build(rfc-0001): add the Docker dev image, Makefile and Go skeleton

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: YAML database and config files

**Files:**
- Create: `internal/db/paths.go`, `internal/db/types.go`, `internal/db/file.go`, `internal/db/config.go`, `internal/db/lock.go`
- Test: `internal/db/paths_test.go`, `internal/db/file_test.go`, `internal/db/config_test.go`, `internal/db/lock_test.go`
- Modify: `go.mod`, `go.sum` (add `go.yaml.in/yaml/v3 v3.0.5`), `README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: everything listed under `internal/db` in "Interfaces between tasks".

- [ ] **Step 1: Add the dependency**

Run: `bin/dev run go get go.yaml.in/yaml/v3@v3.0.5`
Expected: `go.mod` gains `require go.yaml.in/yaml/v3 v3.0.5` and `go.sum` is written.

- [ ] **Step 2: Write the failing tests**

`internal/db/paths_test.go`:

```go
package db

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathsFollowXDG(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/x/data")
	t.Setenv("XDG_STATE_HOME", "relative/ignored")
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config: "/home/u/.config/mirrormark",
		Data:   "/x/data/mirrormark",
		State:  "/home/u/.local/state/mirrormark",
	}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}
	if got := p.ProfileFile("chrome-a"); got != filepath.Join("/x/data/mirrormark/profiles", "chrome-a.yaml") {
		t.Fatalf("ProfileFile = %q", got)
	}
}
```

`internal/db/file_test.go`:

```go
package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *DB {
	return &DB{
		Format:  1,
		Profile: Profile{ID: "chrome-a", Label: "chrome/a", Browser: "chrome", Path: "/u/.config/google-chrome/Default"},
		Unions: map[string]*Member{"work": {Seq: 3, Path: "toolbar/Work", ID: "58", Children: []*Node{
			{Title: "GitHub", URL: "https://github.com/", ID: "57", M: "m-1", By: "chrome/a"},
			{Title: "_deleted", ID: "90", M: "m-2", By: "chrome/a", Children: []*Node{
				{Title: "Old (chrome/b)", Suffix: "chrome/b", URL: "https://old/", ID: "88", M: "m-3", By: "chrome/b"},
			}},
		}}},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles", "chrome-a.yaml")
	if err := SaveDB(path, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDB(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sample()
	if got.Profile != want.Profile || got.Unions["work"].Seq != 3 || !EqualNodes(got.Unions["work"].Children, want.Unions["work"].Children) {
		t.Fatalf("round trip changed the file: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "# Written by mirrormark.") {
		t.Fatalf("missing header: %q", raw[:40])
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := WriteAtomic(filepath.Join(dir, "f.yaml"), []byte{byte('a' + i)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestLoadDBRejectsUnknownFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(path, []byte("format: 2\nprofile: {id: x}\n"), 0o600)
	if _, err := LoadDB(path); err == nil {
		t.Fatal("format 2 accepted")
	}
}

func TestBackupOncePerDayKeeps30(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	if err := SaveDB(p.ProfileFile("chrome-a"), sample()); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 35 {
		for range 2 { // a second call on the same day does nothing
			if err := Backup(p, "chrome-a", day.AddDate(0, 0, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, _ := os.ReadDir(p.BackupDir("chrome-a"))
	if len(entries) != 30 {
		t.Fatalf("got %d backups, want 30", len(entries))
	}
	if entries[0].Name() != "2026-01-06.yaml" {
		t.Fatalf("oldest kept is %s, want 2026-01-06.yaml", entries[0].Name())
	}
}

func TestLoadAllReportsBadFiles(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	SaveDB(p.ProfileFile("chrome-a"), sample())
	os.WriteFile(p.ProfileFile("chrome-b"), []byte("{not yaml"), 0o600)
	dbs, errs := LoadAll(p)
	if dbs["chrome-a"] == nil || errs["chrome-b"] == nil || len(dbs) != 1 {
		t.Fatalf("dbs %v errs %v", dbs, errs)
	}
}
```

`internal/db/config_test.go`:

```go
package db

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSplitPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"toolbar/Work", []string{"toolbar", "Work"}, true},
		{`other/A\/B/C`, []string{"other", "A/B", "C"}, true},
		{`Bookmarks bar/x\\y`, []string{"Bookmarks bar", `x\y`}, true},
		{"toolbar//Work", nil, false},
		{"", nil, false},
		{`toolbar/x\`, nil, false},
	} {
		got, err := SplitPath(c.in)
		if (err == nil) != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("SplitPath(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestLoadConfigMissingIsEmpty(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil || c.Format != 1 || len(c.Unions) != 0 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestConfigValidate(t *testing.T) {
	for name, text := range map[string]string{
		"bad format":  "format: 2\n",
		"bad id":      "format: 1\nunions:\n  work:\n    Chrome_A: toolbar/Work\n",
		"bad path":    "format: 1\nunions:\n  work:\n    chrome-a: toolbar//Work\n",
		"nested":      "format: 1\nunions:\n  a:\n    chrome-a: toolbar/Work\n  b:\n    chrome-a: toolbar/Work/Sub\n",
		"same folder": "format: 1\nunions:\n  a:\n    chrome-a: toolbar/Work\n  b:\n    chrome-a: toolbar/Work\n",
		"unknown key": "format: 1\nunion: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte(text), 0o600)
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("format: 1\nunions:\n  work:\n    chrome-a: toolbar/Work\n    chrome-b: other/Work\n"), 0o600)
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
```

`internal/db/lock_test.go`:

```go
package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: %v, want ErrLocked", err)
	}
	unlock()
	unlock2, err := TryLock(path)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestConnected(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	since := time.Now()
	if Connected(p, "chrome-a", since) {
		t.Fatal("connected without a lock file")
	}
	unlock, err := TryLock(p.ProfileLock("chrome-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	later := since.Add(time.Second)
	if err := os.Chtimes(p.ProfileLock("chrome-a"), later, later); err != nil {
		t.Fatal(err)
	}
	if !Connected(p, "chrome-a", since) {
		t.Fatal("touched lock file not seen")
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `bin/dev run go test ./internal/db/`
Expected: FAIL, undefined names.

- [ ] **Step 4: Write `internal/db/paths.go`**

```go
// Package db reads and writes mirrormark's files: the user's config.yaml,
// one database file per browser profile, backups and locks.
package db

import (
	"os"
	"path/filepath"
)

// Paths holds mirrormark's three directories.
type Paths struct {
	Config string // config.yaml
	Data   string // profiles/, backups/, lock, extension/
	State  string // the host's log
}

// DefaultPaths follows the XDG base directory spec: a variable counts only
// if it holds an absolute path.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	dir := func(env, def string) string {
		if v := os.Getenv(env); filepath.IsAbs(v) {
			return filepath.Join(v, "mirrormark")
		}
		return filepath.Join(home, def, "mirrormark")
	}
	return Paths{
		Config: dir("XDG_CONFIG_HOME", ".config"),
		Data:   dir("XDG_DATA_HOME", ".local/share"),
		State:  dir("XDG_STATE_HOME", ".local/state"),
	}, nil
}

func (p Paths) ConfigFile() string            { return filepath.Join(p.Config, "config.yaml") }
func (p Paths) ProfilesDir() string           { return filepath.Join(p.Data, "profiles") }
func (p Paths) ProfileFile(id string) string  { return filepath.Join(p.ProfilesDir(), id+".yaml") }
func (p Paths) ProfileLock(id string) string  { return filepath.Join(p.ProfilesDir(), id+".lock") }
func (p Paths) GlobalLock() string            { return filepath.Join(p.Data, "lock") }
func (p Paths) BackupDir(id string) string    { return filepath.Join(p.Data, "backups", id) }
func (p Paths) LogFile() string               { return filepath.Join(p.State, "log") }
```

- [ ] **Step 5: Write `internal/db/types.go`**

```go
package db

import (
	"regexp"
	"slices"
)

// ValidID matches database ids. They are file names and appear in bind URLs.
var ValidID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Node is a bookmark (URL set) or a folder (URL empty) inside a member folder.
type Node struct {
	Title    string  `yaml:"title"`
	URL      string  `yaml:"url,omitempty"`
	ID       string  `yaml:"id,omitempty"`     // the browser's node id in this profile
	M        string  `yaml:"m,omitempty"`      // mirrormark's id, the same in every profile
	By       string  `yaml:"by,omitempty"`     // label of the profile that last changed it
	Suffix   string  `yaml:"suffix,omitempty"` // label mirrormark appended to Title as " (label)"
	Children []*Node `yaml:"children,omitempty"`
}

// Member is one profile's record of one union: its member folder's contents
// as of the profile's last completed round.
type Member struct {
	Seq      int64   `yaml:"seq"`  // orders this union's records across members
	Path     string  `yaml:"path"` // the config path the record was made for
	ID       string  `yaml:"id"`   // browser id of the member folder
	Children []*Node `yaml:"children,omitempty"`
}

type Profile struct {
	ID      string `yaml:"id"`
	Label   string `yaml:"label"`
	Browser string `yaml:"browser"`
	Path    string `yaml:"path"`
}

// DB is one profile's database file.
type DB struct {
	Format  int                `yaml:"format"`
	Profile Profile            `yaml:"profile"`
	Unions  map[string]*Member `yaml:"unions,omitempty"`
}

// Config is config.yaml: union name → profile id → folder path.
type Config struct {
	Format int                          `yaml:"format"`
	Unions map[string]map[string]string `yaml:"unions"`
}

// EqualNodes compares two trees field by field, in order.
func EqualNodes(a, b []*Node) bool {
	return slices.EqualFunc(a, b, func(x, y *Node) bool {
		return x.Title == y.Title && x.URL == y.URL && x.ID == y.ID && x.M == y.M &&
			x.By == y.By && x.Suffix == y.Suffix && EqualNodes(x.Children, y.Children)
	})
}
```

- [ ] **Step 6: Write `internal/db/file.go`**

```go
package db

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const dbHeader = "# Written by mirrormark. You may edit the profile block; do not edit below it.\n"

// WriteAtomic writes data to a temporary file in path's directory, flushes
// it, and renames it over path, so a crash leaves the old file or the new one.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func marshal[T *DB | *Config](header string, v T) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unmarshalStrict rejects unknown keys, so a typo in a hand edit is an error.
func unmarshalStrict[T *DB | *Config](data []byte, v T) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

func LoadDB(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d DB
	if err := unmarshalStrict(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if d.Format != 1 {
		return nil, fmt.Errorf("%s: unsupported format %d", path, d.Format)
	}
	if !ValidID.MatchString(d.Profile.ID) {
		return nil, fmt.Errorf("%s: invalid profile id %q", path, d.Profile.ID)
	}
	return &d, nil
}

func SaveDB(path string, d *DB) error {
	data, err := marshal(dbHeader, d)
	if err != nil {
		return err
	}
	return WriteAtomic(path, data, 0o600)
}

// LoadAll reads every database file. Files that fail to load are reported
// by id instead of being skipped silently.
func LoadAll(p Paths) (map[string]*DB, map[string]error) {
	dbs, errs := map[string]*DB{}, map[string]error{}
	names, _ := filepath.Glob(filepath.Join(p.ProfilesDir(), "*.yaml"))
	for _, name := range names {
		id := strings.TrimSuffix(filepath.Base(name), ".yaml")
		d, err := LoadDB(name)
		if err != nil {
			errs[id] = err
			continue
		}
		dbs[id] = d
	}
	return dbs, errs
}

// Backup copies the database file to backups/<id>/<date>.yaml before the
// first write of each day and keeps the newest 30. One backup a day means a
// burst of bad rounds cannot rotate every good copy away.
func Backup(p Paths, id string, now time.Time) error {
	data, err := os.ReadFile(p.ProfileFile(id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	dir := p.BackupDir(id)
	dst := filepath.Join(dir, now.Format("2006-01-02")+".yaml")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := WriteAtomic(dst, data, 0o600); err != nil {
		return err
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	slices.Sort(names)
	for len(names) > 30 {
		if err := os.Remove(names[0]); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
```

`marshal` and `unmarshalStrict` are generic over the three file types so they need no `any` parameter.

- [ ] **Step 7: Write `internal/db/config.go`**

```go
package db

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// SplitPath splits a config folder path on "/". A backslash escapes the
// next character, so `\/` is a slash inside a title.
func SplitPath(p string) ([]string, error) {
	var segs []string
	var cur strings.Builder
	esc := false
	for _, r := range p {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '/':
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if esc {
		return nil, fmt.Errorf("path %q ends in a backslash", p)
	}
	segs = append(segs, cur.String())
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("path %q has an empty folder name", p)
		}
	}
	return segs, nil
}

// LoadConfig reads and validates config.yaml. A missing file is an empty
// config, so a fresh install syncs nothing until the user adds a union.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Format: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := unmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.Format != 1 {
		return fmt.Errorf("unsupported format %d", c.Format)
	}
	byProfile := map[string][][]string{}
	for name, members := range c.Unions {
		if name == "" {
			return errors.New("a union has an empty name")
		}
		for id, path := range members {
			if !ValidID.MatchString(id) {
				return fmt.Errorf("union %s: invalid profile id %q", name, id)
			}
			segs, err := SplitPath(path)
			if err != nil {
				return fmt.Errorf("union %s: %w", name, err)
			}
			byProfile[id] = append(byProfile[id], segs)
		}
	}
	// One profile's member folders may not nest, or one union's edits would
	// be another union's edits too.
	for id, all := range byProfile {
		for i, a := range all {
			for j, b := range all {
				if i != j && len(a) <= len(b) && slices.Equal(a, b[:len(a)]) {
					return fmt.Errorf("profile %s: folder %s is inside or equal to %s", id, strings.Join(b, "/"), strings.Join(a, "/"))
				}
			}
		}
	}
	return nil
}
```

- [ ] **Step 8: Write `internal/db/lock.go`**

```go
package db

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Lock takes an exclusive flock on path, waiting for it. The kernel releases
// it if the process dies, so a crashed host never leaves a stale lock.
func Lock(path string) (func(), error) { return lock(path, 0) }

// TryLock is Lock without waiting; it returns ErrLocked if the lock is held.
func TryLock(path string) (func(), error) { return lock(path, syscall.LOCK_NB) }

func lock(path string, flags int) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|flags); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// Connected reports whether a host has said hello for profile id since
// since. The host touches the profile's lock file on each hello; reading
// its mtime never takes the lock, so it cannot race the host for it.
func Connected(p Paths, id string, since time.Time) bool {
	info, err := os.Stat(p.ProfileLock(id))
	return err == nil && info.ModTime().After(since)
}
```

Hosts poll only `*.yaml` files, so touching a lock file never starts a round.

- [ ] **Step 9: Run the tests**

Run: `bin/dev run go test ./internal/db/`
Expected: PASS.

- [ ] **Step 10: Docs**

Append to `README.md`, before "Development":

```markdown
## Files

| File | What it is |
| --- | --- |
| `~/.config/mirrormark/config.yaml` | Your unions. You write it. |
| `~/.local/share/mirrormark/profiles/<id>.yaml` | One database per bound profile. You may edit its `profile:` block; mirrormark writes the rest. |
| `~/.local/share/mirrormark/backups/<id>/` | One copy of each database a day, the newest 30. For reading only. |
| `~/.local/state/mirrormark/log` | The host's log. |

`$XDG_CONFIG_HOME`, `$XDG_DATA_HOME` and `$XDG_STATE_HOME` move these when set.
Database and backup files are readable only by you (mode 0600); they hold
your bookmarks in plain text.

### config.yaml

    format: 1
    unions:
      work:                            # any name
        chrome-personal: toolbar/Work  # profile id: folder path
        chrome-work: toolbar/Work
      reading:
        chrome-personal: other/Reading
        chrome-work: other/Reading

A path starts with `toolbar`, `other` or `mobile`, or with the title Chrome
shows for that folder, such as `Bookmarks bar`. Write `\/` for a slash inside
a folder name. One profile's folders may not be inside each other.
```

- [ ] **Step 11: Verify**

Run: `bin/dev run make`
Expected: all packages pass, build succeeds.

- [ ] **Step 12: Commit**

```bash
git add go.mod go.sum internal/db README.md
git commit -m "feat(rfc-0001): read and write the YAML database and config files safely [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Merge engine

The heart of the project. Test first: write each test, watch it fail, then make it pass. The fake browser applies ops exactly as the extension will (task 6), so every test also checks that the ops turn P's browser into the merged tree.

**Files:**
- Create: `internal/fakebrowser/fakebrowser.go`, `internal/merge/merge.go`, `internal/merge/ops.go`
- Test: `internal/merge/merge_test.go` (package `merge_test`)
- Modify: `README.md`

**Interfaces:**
- Consumes: `db.Node`.
- Produces: `merge.Round`, `merge.Input`, `merge.Result`, `merge.Op`, `merge.Expect`, `merge.OpResult`, `merge.Conflict`, `merge.ErrWrongTree`, `merge.DeletedTitle`, `merge.NewM`; `fakebrowser.New`, `Add`, `Rename`, `SetURL`, `Move`, `Remove`, `RemoveTree`, `Find`, `Children`, `Lines`, `Apply`, `NodeLines`.

**Semantics to implement** (each has a test below):

- Three trees per union, matched by `m`: base (P's record), ours (P's browser now; nodes carry browser `ID`, no `M`), theirs (the union record with the highest `seq`; may be P's own, then it equals base).
- Ours nodes get their `M` from base by browser id. A suffix stays only while the title still ends in ` (<suffix>)`.
- Per node, fields title, URL, parent merge independently: unchanged/unchanged → base; changed/unchanged → ours with `by` = P's label; unchanged/changed → theirs; both the same → that; both different → conflict.
- Bookmark conflict: theirs keeps `m`, retitled `<title without old suffix> (<theirs.by>)`; P's browser node becomes a new node (fresh `m`) titled `<title without old suffix> (<P label>)`. Folder conflict: theirs wins per conflicting field, recorded in `Conflicts`.
- Adds (not in base): processed top-down under each parent; an ours add and a theirs add with the same title and URL (folders: title) link (ours takes theirs' `m`); same title, both bookmarks, different URLs → both survive suffixed; anything else is added with a fresh `m`.
- A base node missing from ours and not inside `_deleted`: deletion. If its base parent is still in ours (or is the member folder), it moves to `_deleted` (created on first use, with a fresh `m`); otherwise it stays under its (also recreated) parent. It is then merged like an ours edit against theirs, and recreated in P's browser.
- A base node missing from ours and inside `_deleted` (or `_deleted` itself): P purged it. Removed if theirs did not change it; else theirs wins and it is recreated.
- A base node missing from theirs: another member purged it. Removed from P if ours did not change it; else ours wins.
- A removed node with a kept descendant is kept. A kept node whose parent exists nowhere goes to `_deleted`.
- Cycles: undo the move that came from theirs, then keep parents again, until neither changes anything. Every merged node must reach the member folder; if one does not, `Round` returns an error instead of dropping it.
- Guard: with base and more than 5 base nodes, count base nodes missing from ours (inside `_deleted` too) or, outside `_deleted` in base, missing from theirs; count × 2 > base count → `ErrWrongTree`.
- Retry: a node the round would create is linked to an identical new node (same title and URL, not linked to anything else) already in P's browser under the same parent. After a round that failed part-way, this is how creates that did land are recognised, so the retry does not duplicate them.
- Order: nodes P's browser already holds under the same parent keep their order; other nodes are inserted at their source index (clamped).
- Ops: DFS over the merged tree. `create` (Ref = the node's `m`, Parent = parent browser id or parent `m` if that parent is created in the same round) and `move` (Expect.Parent = current browser parent) in DFS order, then `update` (Expect title and URL), then `remove` of every browser node no longer in the tree, children before their folder (Expect title and URL). A remove is non-recursive, like `bookmarks.remove`: the fake browser refuses to remove a non-empty folder, and `Apply` stops at the first failed op and answers the rest `not run`, as the extension does. The member folder's browser id is `Input.RootID`.

- [ ] **Step 1: Write the fake browser** `internal/fakebrowser/fakebrowser.go`

It is test support, but a normal package so tests in several packages can share it.

```go
// Package fakebrowser is an in-memory bookmarks tree that applies mirrormark
// ops the way the extension does. Tests use it in place of Chrome.
package fakebrowser

import (
	"slices"
	"strconv"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
)

type node struct {
	id, title, url string
	parent         *node
	kids           []*node
}

type Browser struct {
	nodes map[string]*node
	next  int
	refs  map[string]string
}

// New returns a browser with Chrome's three top-level folders:
// "1" Bookmarks bar, "2" Other bookmarks, "3" Mobile bookmarks.
func New() *Browser {
	b := &Browser{nodes: map[string]*node{}, refs: map[string]string{}}
	root := b.make(nil, "", "", -1)
	for _, t := range []string{"Bookmarks bar", "Other bookmarks", "Mobile bookmarks"} {
		b.make(root, t, "", -1)
	}
	return b
}

func (b *Browser) make(parent *node, title, url string, index int) *node {
	n := &node{id: strconv.Itoa(b.next), title: title, url: url, parent: parent}
	b.next++
	b.nodes[n.id] = n
	if parent != nil {
		b.insert(parent, n, index)
	}
	return n
}

func (b *Browser) insert(parent, n *node, index int) {
	if index < 0 || index > len(parent.kids) {
		index = len(parent.kids)
	}
	parent.kids = slices.Insert(parent.kids, index, n)
	n.parent = parent
}

func (b *Browser) detach(n *node) {
	p := n.parent
	p.kids = slices.DeleteFunc(p.kids, func(k *node) bool { return k == n })
}

func (b *Browser) forget(n *node) {
	delete(b.nodes, n.id)
	for _, k := range n.kids {
		b.forget(k)
	}
}

// Add appends a bookmark, or a folder when url is empty, and returns its id.
func (b *Browser) Add(parentID, title, url string) string {
	return b.make(b.nodes[parentID], title, url, -1).id
}
func (b *Browser) Rename(id, title string) { b.nodes[id].title = title }
func (b *Browser) SetURL(id, url string)   { b.nodes[id].url = url }
func (b *Browser) Move(id, parentID string) {
	n := b.nodes[id]
	b.detach(n)
	b.insert(b.nodes[parentID], n, -1)
}
func (b *Browser) Remove(id string) {
	n := b.nodes[id]
	b.detach(n)
	b.forget(n)
}

// Find returns the id of the first child of parentID titled title, or "".
func (b *Browser) Find(parentID, title string) string {
	for _, k := range b.nodes[parentID].kids {
		if k.title == title {
			return k.id
		}
	}
	return ""
}

// Children returns the subtree under id as merge input: ID, Title, URL only.
func (b *Browser) Children(id string) []*db.Node {
	var conv func(ns []*node) []*db.Node
	conv = func(ns []*node) []*db.Node {
		var out []*db.Node
		for _, n := range ns {
			out = append(out, &db.Node{ID: n.id, Title: n.title, URL: n.url, Children: conv(n.kids)})
		}
		return out
	}
	return conv(b.nodes[id].kids)
}

// Lines lists the subtree under id as sorted "path url" lines, folders as
// "path/". Sibling order is ignored because mirrormark does not sync it.
func (b *Browser) Lines(id string) []string { return NodeLines(b.Children(id)) }

// NodeLines is Lines for a merge tree.
func NodeLines(ns []*db.Node) []string {
	var out []string
	var walk func(ns []*db.Node, prefix string)
	walk = func(ns []*db.Node, prefix string) {
		for _, n := range ns {
			p := prefix + "/" + n.Title
			if n.URL == "" {
				out = append(out, p+"/")
			} else {
				out = append(out, p+" "+n.URL)
			}
			walk(n.Children, p)
		}
	}
	walk(ns, "")
	slices.Sort(out)
	return out
}

// Apply runs ops one at a time, as the extension does. fail, if not nil,
// makes op i fail without running, to simulate a browser error.
func (b *Browser) Apply(ops []merge.Op, fail func(i int) bool) []merge.OpResult {
	res := make([]merge.OpResult, len(ops))
	for i, op := range ops {
		if fail != nil && fail(i) {
			res[i] = merge.OpResult{Error: "injected"}
			continue
		}
		res[i] = b.apply(op)
	}
	return res
}

func (b *Browser) resolve(id string) *node {
	if r, ok := b.refs[id]; ok {
		id = r
	}
	return b.nodes[id]
}

func (b *Browser) apply(op merge.Op) merge.OpResult {
	stale := merge.OpResult{Error: "stale"}
	index := -1
	if op.Index != nil {
		index = *op.Index
	}
	switch op.Op {
	case "create":
		p := b.resolve(op.Parent)
		if p == nil {
			return merge.OpResult{Error: "no parent"}
		}
		url := ""
		if op.URL != nil {
			url = *op.URL
		}
		n := b.make(p, *op.Title, url, index)
		b.refs[op.Ref] = n.id
		return merge.OpResult{OK: true, ID: n.id}
	case "update":
		n := b.nodes[op.ID]
		if n == nil || (op.Expect.Title != nil && *op.Expect.Title != n.title) || (op.Expect.URL != nil && *op.Expect.URL != n.url) {
			return stale
		}
		if op.Title != nil {
			n.title = *op.Title
		}
		if op.URL != nil {
			n.url = *op.URL
		}
		return merge.OpResult{OK: true}
	case "move":
		n, p := b.nodes[op.ID], b.resolve(op.Parent)
		if n == nil || p == nil || n.parent.id != op.Expect.Parent {
			return stale
		}
		for a := p; a != nil; a = a.parent {
			if a == n {
				return merge.OpResult{Error: "move into itself"}
			}
		}
		b.detach(n)
		b.insert(p, n, index)
		return merge.OpResult{OK: true}
	case "remove":
		n := b.nodes[op.ID]
		if n == nil || *op.Expect.Title != n.title || *op.Expect.URL != n.url {
			return stale
		}
		b.Remove(op.ID)
		return merge.OpResult{OK: true}
	}
	return merge.OpResult{Error: "unknown op " + op.Op}
}
```

- [ ] **Step 2: Write `internal/merge/ops.go`** (types only, so the fake browser and tests compile)

```go
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
```

- [ ] **Step 3: Write the failing tests** `internal/merge/merge_test.go`

```go
package merge_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
)

const (
	me    = "chrome/personal"
	other = "chrome/work"
)

// fixture is profile P's browser with member folder "Work" on the bar.
type fixture struct {
	t    *testing.T
	b    *fakebrowser.Browser
	root string
	base []*db.Node
}

// newFixture builds P's browser with fill, then records it as base: every
// node gets m "m-<title>" and by "chrome/work".
func newFixture(t *testing.T, fill func(b *fakebrowser.Browser, root string)) *fixture {
	b := fakebrowser.New()
	root := b.Add("1", "Work", "")
	fill(b, root)
	base := b.Children(root)
	var mark func(ns []*db.Node)
	mark = func(ns []*db.Node) {
		for _, n := range ns {
			n.M, n.By = "m-"+n.Title, other
			mark(n.Children)
		}
	}
	mark(base)
	return &fixture{t: t, b: b, root: root, base: base}
}

// theirs returns a copy of base, changed by edit, without browser ids.
func (f *fixture) theirs(edit func(ns []*db.Node) []*db.Node) []*db.Node {
	var cp func(ns []*db.Node) []*db.Node
	cp = func(ns []*db.Node) []*db.Node {
		var out []*db.Node
		for _, n := range ns {
			c := *n
			c.ID = ""
			c.Children = cp(n.Children)
			out = append(out, &c)
		}
		return out
	}
	return edit(cp(f.base))
}

func find(ns []*db.Node, title string) *db.Node {
	for _, n := range ns {
		if n.Title == title {
			return n
		}
		if c := find(n.Children, title); c != nil {
			return c
		}
	}
	return nil
}

func freshMs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("m-new%d", n) }
}

// round merges, applies the ops to P's browser, and checks that the browser
// now matches the merged tree.
func (f *fixture) round(theirs []*db.Node) merge.Result {
	f.t.Helper()
	res, err := merge.Round(merge.Input{Base: f.base, HasBase: f.base != nil, Ours: f.b.Children(f.root),
		Theirs: theirs, RootID: f.root, Label: me, NewM: freshMs()})
	if err != nil {
		f.t.Fatalf("Round: %v", err)
	}
	for i, r := range f.b.Apply(res.Ops, nil) {
		if !r.OK {
			f.t.Fatalf("op %d %+v failed: %s", i, res.Ops[i], r.Error)
		}
	}
	if got, want := f.b.Lines(f.root), fakebrowser.NodeLines(res.Tree); !slices.Equal(got, want) {
		f.t.Fatalf("browser after ops:\n%q\nmerged tree:\n%q", got, want)
	}
	return res
}

func (f *fixture) want(want ...string) {
	f.t.Helper()
	slices.Sort(want)
	if got := f.b.Lines(f.root); !slices.Equal(got, want) {
		f.t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func gh(b *fakebrowser.Browser, root string) { b.Add(root, "GitHub", "https://github.com/") }

func TestQuietRoundChangesNothing(t *testing.T) {
	f := newFixture(t, gh)
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	if len(res.Ops) != 0 {
		t.Fatalf("ops %+v, want none", res.Ops)
	}
	if !db.EqualNodes(res.Tree, f.base) {
		t.Fatalf("tree %+v differs from base", res.Tree)
	}
}

func TestTheirChangeIsApplied(t *testing.T) {
	f := newFixture(t, gh)
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GitHub https://github.com/orgs/b")
}

func TestOurChangeIsKeptAndSigned(t *testing.T) {
	f := newFixture(t, gh)
	f.b.Rename(f.b.Find(f.root, "GitHub"), "GH")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	if len(res.Ops) != 0 || res.Tree[0].By != me || res.Tree[0].M != "m-GitHub" {
		t.Fatalf("ops %+v tree %+v", res.Ops, res.Tree[0])
	}
}

func TestSameChangeOnBothSides(t *testing.T) {
	f := newFixture(t, gh)
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/x")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/x"; return ns }))
	if len(res.Ops) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("ops %+v conflicts %+v", res.Ops, res.Conflicts)
	}
}

func TestURLConflictMakesSuffixedCopies(t *testing.T) {
	f := newFixture(t, gh)
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/orgs/a")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GitHub (chrome/personal) https://github.com/orgs/a", "/GitHub (chrome/work) https://github.com/orgs/b")
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if n := find(res.Tree, "GitHub (chrome/work)"); n.M != "m-GitHub" || n.Suffix != other {
		t.Fatalf("theirs copy %+v should keep m and carry the suffix", n)
	}
	if n := find(res.Tree, "GitHub (chrome/personal)"); n.M == "m-GitHub" || n.ID == "" || n.Suffix != me {
		t.Fatalf("our copy %+v should be the browser's node with a new m", n)
	}
}

func TestTitleAndURLEditsCombine(t *testing.T) {
	f := newFixture(t, gh)
	f.b.Rename(f.b.Find(f.root, "GitHub"), "GH")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns }))
	f.want("/GH https://github.com/orgs/b")
}

func news(b *fakebrowser.Browser, root string) { b.Add(root, "News", "https://lwn.net/") }

func TestDeleteMovesToDeleted(t *testing.T) {
	f := newFixture(t, news)
	f.b.Remove(f.b.Find(f.root, "News"))
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/_deleted/", "/_deleted/News https://lwn.net/")
	if n := find(res.Tree, "News"); n.M != "m-News" {
		t.Fatalf("deleted node lost its m: %+v", n)
	}
}

func TestDeleteKeepsTheirRename(t *testing.T) {
	f := newFixture(t, news)
	f.b.Remove(f.b.Find(f.root, "News"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].Title = "LWN"; return ns }))
	f.want("/_deleted/", "/_deleted/LWN https://lwn.net/")
}

func TestDeletedFolderKeepsItsSubtree(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		d := b.Add(root, "Dev", "")
		b.Add(d, "Go", "https://go.dev/")
		b.Add(root, "A", "https://a/")
		b.Add(root, "B", "https://b/")
	})
	f.b.Remove(f.b.Find(f.root, "Dev"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/A https://a/", "/B https://b/", "/_deleted/", "/_deleted/Dev/", "/_deleted/Dev/Go https://go.dev/")
}

func TestSameTitleAddsConflict(t *testing.T) {
	f := newFixture(t, func(*fakebrowser.Browser, string) {})
	f.b.Add(f.root, "Mail", "https://fastmail.com/")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return append(ns, &db.Node{Title: "Mail", URL: "https://gmail.com/", M: "m-t1", By: other})
	}))
	f.want("/Mail (chrome/personal) https://fastmail.com/", "/Mail (chrome/work) https://gmail.com/")
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestIdenticalAddsLink(t *testing.T) {
	f := newFixture(t, func(*fakebrowser.Browser, string) {})
	d := f.b.Add(f.root, "Dev", "")
	f.b.Add(d, "Go", "https://go.dev/")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return append(ns, &db.Node{Title: "Dev", M: "m-t1", By: other, Children: []*db.Node{
			{Title: "Go", URL: "https://go.dev/", M: "m-t2", By: other},
			{Title: "Rust", URL: "https://rust-lang.org/", M: "m-t3", By: other},
		}})
	}))
	f.want("/Dev/", "/Dev/Go https://go.dev/", "/Dev/Rust https://rust-lang.org/")
	if find(res.Tree, "Dev").M != "m-t1" || find(res.Tree, "Go").M != "m-t2" {
		t.Fatalf("adds did not link: %+v", res.Tree)
	}
}

func TestFirstRoundUnionsAndDeletesNothing(t *testing.T) {
	b := fakebrowser.New()
	root := b.Add("1", "Work", "")
	b.Add(root, "GitHub", "https://github.com/")
	b.Add(root, "Mail", "https://fastmail.com/")
	theirs := []*db.Node{
		{Title: "GitHub", URL: "https://github.com/", M: "m-1", By: other},
		{Title: "Mail", URL: "https://gmail.com/", M: "m-2", By: other},
		{Title: "News", URL: "https://lwn.net/", M: "m-3", By: other},
	}
	f := &fixture{t: t, b: b, root: root}
	f.round(theirs)
	f.want("/GitHub https://github.com/", "/Mail (chrome/personal) https://fastmail.com/",
		"/Mail (chrome/work) https://gmail.com/", "/News https://lwn.net/")
}

func inDeleted(b *fakebrowser.Browser, root string) {
	d := b.Add(root, "_deleted", "")
	b.Add(d, "Old", "https://old/")
	b.Add(root, "Keep", "https://keep/")
}

func TestOurPurgeRemovesIt(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.Remove(f.b.Find(f.b.Find(f.root, "_deleted"), "Old"))
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/Keep https://keep/", "/_deleted/")
	if find(res.Tree, "Old") != nil {
		t.Fatal("purged node is still in the tree")
	}
}

func TestTheirPurgeRemovesItHere(t *testing.T) {
	f := newFixture(t, inDeleted)
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		find(ns, "_deleted").Children = nil
		return ns
	}))
	f.want("/Keep https://keep/", "/_deleted/")
	if len(res.Ops) != 1 || res.Ops[0].Op != "remove" {
		t.Fatalf("ops %+v, want one remove", res.Ops)
	}
}

func TestEditBeatsPurge(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.Remove(f.b.Find(f.b.Find(f.root, "_deleted"), "Old"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { find(ns, "Old").Title = "Older"; return ns }))
	f.want("/Keep https://keep/", "/_deleted/", "/_deleted/Older https://old/")
}

func TestRemovingDeletedFolderPurgesItsContents(t *testing.T) {
	f := newFixture(t, inDeleted)
	f.b.Remove(f.b.Find(f.root, "_deleted"))
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { return ns }))
	f.want("/Keep https://keep/")
}

func TestSecondConflictReplacesSuffix(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "GitHub (chrome/work)", "https://x/") })
	f.base[0].Suffix = other
	f.b.SetURL(f.b.Find(f.root, "GitHub (chrome/work)"), "https://a/")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL, ns[0].By = "https://b/", "chrome/third"; return ns }))
	f.want("/GitHub (chrome/personal) https://a/", "/GitHub (chrome/third) https://b/")
}

func TestTypedParenthesesAreNotASuffix(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "Notes (work)", "https://x/") })
	f.b.SetURL(f.b.Find(f.root, "Notes (work)"), "https://a/")
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://b/"; return ns }))
	f.want("/Notes (work) (chrome/personal) https://a/", "/Notes (work) (chrome/work) https://b/")
}

func TestFolderConflictTheirsWins(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(b.Add(root, "Dev", ""), "Go", "https://go.dev/") })
	f.b.Rename(f.b.Find(f.root, "Dev"), "Code")
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node { ns[0].Title = "Programming"; return ns }))
	f.want("/Programming/", "/Programming/Go https://go.dev/")
	if len(res.Conflicts) != 1 || !res.Conflicts[0].Folder {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestCycleUndoesTheirMove(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "X", ""); b.Add(root, "Y", "") })
	f.b.Move(f.b.Find(f.root, "X"), f.b.Find(f.root, "Y")) // ours: X inside Y
	f.round(f.theirs(func(ns []*db.Node) []*db.Node { // theirs: Y inside X
		x, y := ns[0], ns[1]
		x.Children = []*db.Node{y}
		return []*db.Node{x}
	}))
	f.want("/Y/", "/Y/X/")
}

func TestWrongTreeGuard(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) {
		for i := range 8 {
			b.Add(root, fmt.Sprintf("B%d", i), fmt.Sprintf("https://b/%d", i))
		}
	})
	for i := range 5 {
		f.b.Remove(f.b.Find(f.root, fmt.Sprintf("B%d", i)))
	}
	_, err := merge.Round(merge.Input{Base: f.base, HasBase: true, Ours: f.b.Children(f.root),
		Theirs: f.base, RootID: f.root, Label: me, NewM: freshMs()})
	if !errors.Is(err, merge.ErrWrongTree) {
		t.Fatalf("err %v, want ErrWrongTree", err)
	}
}

func TestRetryAfterPartialFailureDoesNotDuplicate(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { gh(b, root); news(b, root) })
	f.b.Remove(f.b.Find(f.root, "News"))
	f.b.SetURL(f.b.Find(f.root, "GitHub"), "https://github.com/orgs/a")
	theirs := f.theirs(func(ns []*db.Node) []*db.Node { ns[0].URL = "https://github.com/orgs/b"; return ns })
	res, err := merge.Round(merge.Input{Base: f.base, HasBase: true, Ours: f.b.Children(f.root),
		Theirs: theirs, RootID: f.root, Label: me, NewM: freshMs()})
	if err != nil {
		t.Fatal(err)
	}
	// Every create lands and every other op fails, as if the browser
	// stopped answering mid-batch. Base stays as it was: nothing was written.
	f.b.Apply(res.Ops, func(i int) bool { return res.Ops[i].Op != "create" })
	f.round(theirs)
	f.want("/GitHub (chrome/personal) https://github.com/orgs/a", "/GitHub (chrome/work) https://github.com/orgs/b",
		"/_deleted/", "/_deleted/News https://lwn.net/")
}

func TestNewBookmarkTakesItsSourcePosition(t *testing.T) {
	f := newFixture(t, func(b *fakebrowser.Browser, root string) { b.Add(root, "A", "https://a/"); b.Add(root, "C", "https://c/") })
	res := f.round(f.theirs(func(ns []*db.Node) []*db.Node {
		return slices.Insert(ns, 1, &db.Node{Title: "B", URL: "https://b/", M: "m-B", By: other})
	}))
	var titles []string
	for _, n := range res.Tree {
		titles = append(titles, n.Title)
	}
	if !slices.Equal(titles, []string{"A", "B", "C"}) {
		t.Fatalf("order %q", titles)
	}
}
```

- [ ] **Step 4: Run them to see them fail**

Run: `bin/dev run go test ./internal/merge/`
Expected: FAIL, `undefined: merge.Round` (and friends).

- [ ] **Step 5: Write `internal/merge/merge.go`**

```go
// Package merge computes one profile's round for one union: a three-way
// merge of what the profile last published (base), what its browser holds
// now (ours) and the union's latest published state (theirs). It does no I/O.
package merge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/chriscz/mirrormark/internal/db"
)

// DeletedTitle names the folder, directly inside each member folder, that
// holds deleted bookmarks until the user empties it.
const DeletedTitle = "_deleted"

// ErrWrongTree means the round would move most of a member folder into
// _deleted or remove it. That is far more often a browser that has not
// loaded its bookmarks, a reset profile, a wrong binding or a damaged file
// than a real mass delete.
var ErrWrongTree = errors.New("db-mismatch: more than half of the member folder would move to " + DeletedTitle +
	" or be purged; is a database file truncated, or this profile bound to the wrong one?")

type Input struct {
	Base    []*db.Node // P's record; nil and HasBase false on P's first round
	HasBase bool
	Ours    []*db.Node // P's member folder now: ID, Title, URL only
	Theirs  []*db.Node // the union's latest record; nil if none yet
	RootID  string     // browser id of P's member folder
	Label   string     // P's label
	NewM    func() string
}

// Conflict records both versions of a node two profiles changed differently.
type Conflict struct {
	Folder       bool
	Ours, Theirs string // "title url" as each side had it
}

type Result struct {
	Tree      []*db.Node // the union as P's browser will hold it; ID "" until created
	Ops       []Op
	Conflicts []Conflict
}

// NewM returns a fresh mirrormark id.
func NewM() string {
	var b [6]byte
	rand.Read(b[:])
	return "m-" + hex.EncodeToString(b[:])
}

// last is past every real index, so a node placed there goes to the end.
const last = 1 << 30

type val struct {
	title, url, suffix, by string
	parent                 string // m of the parent; "" is the member folder
	folder                 bool
}

type entry struct {
	v     val
	index int
	id    string
}

// node is one entry of the merged union.
type node struct {
	val
	id          string // P's browser id; "" means create it
	index       int    // position among the source's siblings
	inOurs      bool   // P's browser already holds it under this parent
	theirsMoved bool   // the parent came from theirs
	oursParent  string
}

type bnode struct{ title, url, parent string }

type merger struct {
	in                 Input
	base, ours, theirs map[string]entry
	theirsKids         map[string][]string
	browser            map[string]bnode // P's browser before the round, by id
	browserOrder       []string
	out, dropped       map[string]*node
	oursNew            map[string]bool // fresh m given to an unlinked add in ours
	retitle            map[string]val
	conflicts          []Conflict
	deletions          int
	deleted            string
}

func Round(in Input) (Result, error) {
	m := &merger{in: in, browser: map[string]bnode{}, out: map[string]*node{},
		dropped: map[string]*node{}, retitle: map[string]val{}, theirsKids: map[string][]string{}, oursNew: map[string]bool{}}
	m.indexBrowser(in.Ours, in.RootID)
	m.base = flatten(in.Base)
	m.theirs = flatten(in.Theirs)
	for _, k := range slices.Sorted(maps.Keys(m.theirs)) {
		p := m.theirs[k].v.parent
		m.theirsKids[p] = append(m.theirsKids[p], k)
	}
	for _, kids := range m.theirsKids {
		slices.SortFunc(kids, func(a, b string) int { return m.theirs[a].index - m.theirs[b].index })
	}
	ours := clone(in.Ours)
	m.assign(ours)
	m.linkAdds("", ours)
	m.ours = flatten(ours)

	keys := map[string]bool{}
	for _, s := range []map[string]entry{m.base, m.ours, m.theirs} {
		for k := range s {
			keys[k] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		m.mergeOne(k)
	}
	if in.HasBase && len(m.base) > 5 && 2*m.deletions > len(m.base) {
		return Result{}, ErrWrongTree
	}
	m.resurrect()
	m.breakCycles()
	kids := map[string][]string{}
	for _, k := range slices.Sorted(maps.Keys(m.out)) {
		p := m.out[k].parent
		kids[p] = append(kids[p], k)
	}
	m.dedupe(kids)
	tree := m.build("", kids)
	return Result{Tree: tree, Ops: m.ops(tree), Conflicts: m.conflicts}, nil
}

func clone(ns []*db.Node) []*db.Node {
	var out []*db.Node
	for _, n := range ns {
		c := *n
		c.Children = clone(n.Children)
		out = append(out, &c)
	}
	return out
}

func flatten(ns []*db.Node) map[string]entry {
	out := map[string]entry{}
	var walk func(ns []*db.Node, parent string)
	walk = func(ns []*db.Node, parent string) {
		for i, n := range ns {
			out[n.M] = entry{v: val{title: n.Title, url: n.URL, suffix: n.Suffix, by: n.By, parent: parent, folder: n.URL == ""}, index: i, id: n.ID}
			walk(n.Children, n.M)
		}
	}
	walk(ns, "")
	return out
}

func (m *merger) indexBrowser(ns []*db.Node, parent string) {
	for _, n := range ns {
		m.browser[n.ID] = bnode{title: n.Title, url: n.URL, parent: parent}
		m.browserOrder = append(m.browserOrder, n.ID)
		m.indexBrowser(n.Children, n.ID)
	}
}

// assign gives P's browser nodes their m from base, matched by browser id.
func (m *merger) assign(ns []*db.Node) {
	byID := map[string]string{}
	for k, e := range m.base {
		byID[e.id] = k
	}
	var walk func(ns []*db.Node)
	walk = func(ns []*db.Node) {
		for _, n := range ns {
			if k, ok := byID[n.ID]; ok {
				b := m.base[k].v
				n.M, n.By = k, b.by
				if b.suffix != "" && strings.HasSuffix(n.Title, " ("+b.suffix+")") {
					n.Suffix = b.suffix
				}
			}
			walk(n.Children)
		}
	}
	walk(ns)
}

// linkAdds gives each node new in ours an m: theirs' m for an identical add
// under the same parent, a fresh one otherwise. Top-down, so a linked
// folder's new children can link too.
func (m *merger) linkAdds(parent string, ns []*db.Node) {
	var fresh []string
	for _, k := range m.theirsKids[parent] {
		if _, inBase := m.base[k]; !inBase {
			fresh = append(fresh, k)
		}
	}
	for _, n := range ns {
		if n.M == "" {
			n.M = m.matchAdd(n, &fresh)
		}
		m.linkAdds(n.M, n.Children)
	}
}

func (m *merger) matchAdd(n *db.Node, fresh *[]string) string {
	for i, k := range *fresh {
		if t := m.theirs[k].v; t.title == n.Title && t.url == n.URL {
			*fresh = slices.Delete(*fresh, i, i+1)
			n.By, n.Suffix = t.by, t.suffix
			return k
		}
	}
	if n.URL != "" {
		for i, k := range *fresh {
			if t := m.theirs[k].v; !t.folder && t.title == n.Title {
				*fresh = slices.Delete(*fresh, i, i+1)
				m.retitle[k] = suffixed(t, t.by)
				o := suffixed(val{title: n.Title, suffix: n.Suffix}, m.in.Label)
				n.Title, n.Suffix, n.By = o.title, o.suffix, m.in.Label
				m.conflicts = append(m.conflicts, Conflict{Ours: n.Title + " " + n.URL, Theirs: t.title + " " + t.url})
				return m.in.NewM()
			}
		}
	}
	n.By = m.in.Label
	k := m.in.NewM()
	m.oursNew[k] = true
	return k
}

// suffixed retitles v as "<title> (<label>)", replacing a suffix mirrormark
// added before instead of stacking a second one.
func suffixed(v val, label string) val {
	if v.suffix != "" {
		v.title = strings.TrimSuffix(v.title, " ("+v.suffix+")")
	}
	v.title += " (" + label + ")"
	v.suffix = label
	return v
}

func same(a, b val) bool { return a.title == b.title && a.url == b.url && a.parent == b.parent }

func (m *merger) put(k string, v val, id string, index int, inOurs, theirsMoved bool) *node {
	n := &node{val: v, id: id, index: index, inOurs: inOurs, theirsMoved: theirsMoved}
	m.out[k] = n
	return n
}

func (m *merger) mergeOne(k string) {
	b, inB := m.base[k]
	o, inO := m.ours[k]
	t, inT := m.theirs[k]
	switch {
	case inB && !inO && !inT:
		// gone everywhere
	case inB && !inO:
		if m.inDeleted(k) { // P purged it
			if same(b.v, t.v) {
				m.dropped[k] = &node{val: t.v, index: t.index}
				return
			}
			m.put(k, t.v, "", t.index, false, true)
			return
		}
		// P deleted it: that is a move into _deleted, merged like any edit.
		m.deletions++
		vo := b.v
		if _, ok := m.ours[b.v.parent]; ok || b.v.parent == "" {
			vo.parent = m.deletedM()
		}
		m.merge3(k, b.v, vo, t.v, "", last, t.index)
	case inB && !inT: // another member purged it
		if same(b.v, o.v) {
			m.dropped[k] = &node{val: o.v, id: o.id, index: o.index, inOurs: true}
			return
		}
		v := o.v
		v.by = m.in.Label
		m.put(k, v, o.id, o.index, true, false)
	case inB:
		m.merge3(k, b.v, o.v, t.v, o.id, o.index, t.index)
	case inO && inT: // the same add on both sides, linked
		m.put(k, o.v, o.id, o.index, true, false)
	case inO:
		m.put(k, o.v, o.id, o.index, true, false)
	default: // added by another member
		v := t.v
		if r, ok := m.retitle[k]; ok {
			v = r
		}
		m.put(k, v, "", t.index, false, true)
	}
}

func (m *merger) merge3(k string, b, o, t val, id string, oIndex, tIndex int) {
	const fromBase, fromOurs, fromTheirs = 0, 1, 2
	field := func(bf, of, tf string) (string, int, bool) {
		switch {
		case of == bf && tf == bf:
			return bf, fromBase, true
		case tf == bf:
			return of, fromOurs, true
		case of == bf:
			return tf, fromTheirs, true
		case of == tf:
			return of, fromOurs, true
		}
		return "", 0, false
	}
	title, ft, ok1 := field(b.title, o.title, t.title)
	url, fu, ok2 := field(b.url, o.url, t.url)
	parent, fp, ok3 := field(b.parent, o.parent, t.parent)
	if !ok1 || !ok2 || !ok3 {
		c := Conflict{Folder: b.folder, Ours: o.title + " " + o.url, Theirs: t.title + " " + t.url}
		m.conflicts = append(m.conflicts, c)
		if !b.folder {
			// Both survive: theirs keeps m, P's browser node becomes the copy.
			m.put(k, suffixed(t, t.by), "", tIndex, false, true)
			ov := suffixed(o, m.in.Label)
			ov.by = m.in.Label
			m.put(m.in.NewM(), ov, id, oIndex, id != "", false)
			return
		}
		// Folders are never copied, because a copy would duplicate the
		// subtree. Theirs wins: it is already in the other profiles.
		if !ok1 {
			title, ft = t.title, fromTheirs
		}
		if !ok2 {
			url, fu = t.url, fromTheirs
		}
		if !ok3 {
			parent, fp = t.parent, fromTheirs
		}
	}
	r := val{title: title, url: url, parent: parent, folder: b.folder, by: b.by,
		suffix: [3]string{b.suffix, o.suffix, t.suffix}[ft]}
	switch {
	case ft == fromOurs || fu == fromOurs || fp == fromOurs:
		r.by = m.in.Label
	case ft == fromTheirs || fu == fromTheirs || fp == fromTheirs:
		r.by = t.by
	}
	index := oIndex
	if fp == fromTheirs {
		index = tIndex
	}
	n := m.put(k, r, id, index, id != "" && fp != fromTheirs, fp == fromTheirs)
	n.oursParent = o.parent
}

// inDeleted reports whether base node k is _deleted or inside it.
func (m *merger) inDeleted(k string) bool {
	for range len(m.base) + 1 { // bounded, in case a hand edit made a loop
		e, ok := m.base[k]
		if !ok {
			return false
		}
		if e.v.parent == "" && e.v.folder && e.v.title == DeletedTitle {
			return true
		}
		k = e.v.parent
	}
	return false
}

// deletedM returns the m of the _deleted folder, choosing a fresh one if no
// side has it. resurrect creates the folder if the merge did not keep it.
func (m *merger) deletedM() string {
	if m.deleted != "" {
		return m.deleted
	}
	for _, s := range []map[string]entry{m.ours, m.theirs, m.base} {
		for _, k := range slices.Sorted(maps.Keys(s)) {
			if v := s[k].v; v.parent == "" && v.folder && v.title == DeletedTitle {
				m.deleted = k
				return k
			}
		}
	}
	m.deleted = m.in.NewM()
	return m.deleted
}

// resurrect keeps every parent of a kept node: a dropped parent comes back,
// a missing _deleted is created, and anything else missing sends the node
// to _deleted. A merge never loses a node by losing its parent.
func (m *merger) resurrect() {
	for changed := true; changed; {
		changed = false
		for _, k := range slices.Sorted(maps.Keys(m.out)) {
			n := m.out[k]
			if n.parent == "" {
				continue
			}
			if _, ok := m.out[n.parent]; ok {
				continue
			}
			changed = true
			switch d, ok := m.dropped[n.parent]; {
			case ok:
				m.out[n.parent] = d
				delete(m.dropped, n.parent)
			case n.parent == m.deleted:
				m.put(m.deleted, val{title: DeletedTitle, folder: true, by: m.in.Label}, "", last, false, false)
			default:
				n.parent, n.inOurs, n.index = m.deletedM(), false, last
			}
		}
	}
}

// breakCycles undoes, in each cycle, a move that came from theirs. A cycle
// needs one move from each side (A moves X into Y, B moves Y into X).
func (m *merger) breakCycles() {
	for found := true; found; {
		found = false
		for _, k := range slices.Sorted(maps.Keys(m.out)) {
			seen := map[string]bool{}
			p := k
			for p != "" && !seen[p] {
				seen[p] = true
				p = m.out[p].parent
			}
			if p == "" {
				continue
			}
			found = true
			c := p
			for !m.out[c].theirsMoved {
				c = m.out[c].parent
				if c == p { // no move from theirs: cannot happen with valid input
					m.out[p].parent = m.deletedM()
					break
				}
			}
			if n := m.out[c]; n.theirsMoved {
				n.parent, n.theirsMoved, n.inOurs = n.oursParent, false, n.id != ""
			}
			break
		}
	}
}

// dedupe links each node this round would create to an identical unlinked
// add in P's browser under the same parent, top-down. After a round that
// failed part-way, the creates that landed show up as such adds; linking
// them keeps the retry from creating them twice.
func (m *merger) dedupe(kids map[string][]string) {
	queue := []string{""}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, k := range slices.Clone(kids[p]) {
			n, ok := m.out[k]
			if !ok || n.id != "" {
				continue
			}
			i := slices.IndexFunc(kids[p], func(j string) bool {
				o := m.out[j]
				return m.oursNew[j] && o.title == n.title && o.url == n.url
			})
			if i < 0 {
				continue
			}
			j := kids[p][i]
			o := m.out[j]
			n.id, n.inOurs, n.index = o.id, true, o.index
			for _, c := range kids[j] {
				m.out[c].parent = k
			}
			kids[k] = append(kids[k], kids[j]...)
			delete(kids, j)
			delete(m.out, j)
			delete(m.oursNew, j)
			kids[p] = slices.Delete(kids[p], i, i+1)
		}
		queue = append(queue, kids[p]...)
	}
}

func (m *merger) build(parent string, kids map[string][]string) []*db.Node {
	var ordered, rest []string
	for _, k := range kids[parent] {
		if m.out[k].inOurs {
			ordered = append(ordered, k)
		} else {
			rest = append(rest, k)
		}
	}
	byIndex := func(a, b string) int { return m.out[a].index - m.out[b].index }
	slices.SortStableFunc(ordered, byIndex)
	slices.SortStableFunc(rest, byIndex)
	for _, k := range rest {
		ordered = slices.Insert(ordered, min(m.out[k].index, len(ordered)), k)
	}
	var out []*db.Node
	for _, k := range ordered {
		n := m.out[k]
		out = append(out, &db.Node{Title: n.title, URL: n.url, ID: n.id, M: k, By: n.by, Suffix: n.suffix, Children: m.build(k, kids)})
	}
	return out
}

// ops turns P's browser into tree: creates and moves top-down, so a parent
// exists before its children, then updates, then removes last, so nothing
// still needed is inside a removed folder.
func (m *merger) ops(tree []*db.Node) []Op {
	var seq, upd, rem []Op
	kept := map[string]bool{}
	var walk func(parent string, ns []*db.Node)
	walk = func(parent string, ns []*db.Node) {
		for i, n := range ns {
			if n.ID == "" {
				op := Op{Op: "create", Ref: n.M, Parent: parent, Index: new(i), Title: new(n.Title)}
				if n.URL != "" {
					op.URL = new(n.URL)
				}
				seq = append(seq, op)
				walk(n.M, n.Children)
				continue
			}
			kept[n.ID] = true
			b := m.browser[n.ID]
			if b.parent != parent {
				seq = append(seq, Op{Op: "move", ID: n.ID, Parent: parent, Index: new(i), Expect: &Expect{Parent: b.parent}})
			}
			if b.title != n.Title || b.url != n.URL {
				op := Op{Op: "update", ID: n.ID, Expect: &Expect{Title: new(b.title), URL: new(b.url)}}
				if b.title != n.Title {
					op.Title = new(n.Title)
				}
				if b.url != n.URL {
					op.URL = new(n.URL)
				}
				upd = append(upd, op)
			}
			walk(n.ID, n.Children)
		}
	}
	walk(m.in.RootID, tree)
	for _, id := range m.browserOrder {
		b := m.browser[id]
		if !kept[id] && (b.parent == m.in.RootID || kept[b.parent]) {
			rem = append(rem, Op{Op: "remove", ID: id, Expect: &Expect{Title: new(b.title), URL: new(b.url)}})
		}
	}
	return slices.Concat(seq, upd, rem)
}
```

`new(expr)` needs Go 1.26 or later; the image has 1.27.2.

- [ ] **Step 6: Run the tests until they pass**

Run: `bin/dev run go test ./internal/merge/ -v`
Expected: every test PASS. When one fails, fix `merge.go`, not the test, unless the test contradicts the semantics list above; then stop and raise it.

- [ ] **Step 7: Docs**

Append to `README.md` (before "Development"):

```markdown
## How syncing behaves

- A union holds everything any member holds, at the same relative paths.
- Deleting a bookmark in a union moves it to a `_deleted` folder inside the
  union, in every profile. Restore it by moving it out. Delete it from
  `_deleted` to remove it everywhere.
- When two profiles change the same bookmark differently, you get both,
  titled for example `GitHub (chrome/personal)` and `GitHub (chrome/work)`.
  For folders, the change that reached the files first wins.
- Reordering bookmarks inside a folder stays local.
```

- [ ] **Step 8: Verify**

Run: `bin/dev run make`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/merge internal/fakebrowser README.md
git commit -m "feat(rfc-0001): merge a union three ways into suffixed copies [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Round planning and convergence

`round.Make` is everything a host does in one round except I/O: pick each union's records, resolve the member folder in the live tree, create missing member folders, merge, and work out `seq`. Because it is pure, a property test can run many members, random edits, random round orders and injected failures, and check that the union converges and nothing is lost.

**Files:**
- Create: `internal/round/live.go`, `internal/round/round.go`
- Test: `internal/round/round_test.go`, `internal/round/property_test.go` (package `round_test`), `internal/round/export_test.go`
- Modify: `internal/fakebrowser/fakebrowser.go` (add `Tree`); `internal/fakebrowser/fuzz.go` and `internal/merge/fuzz_test.go` (the merge fuzz driver moves to `fakebrowser.Fuzz` so the property test reuses it)

**Interfaces:**
- Consumes: `db.Config`, `db.DB`, `db.Member`, `db.SplitPath`, `db.EqualNodes`, `merge.Round`, `merge.Input`, `merge.Op`, `merge.Conflict`, `merge.ErrWrongTree`; `fakebrowser`.
- Produces: `round.TreeNode`, `round.Live`, `round.Build`, `round.Input`, `round.Plan`, `round.Make`, `Plan.Finish`; `(*fakebrowser.Browser).Tree() []round.TreeNode`; `fakebrowser.Union`, `fakebrowser.Fuzz`, `fakebrowser.Seeds`.

- [ ] **Step 1: Write `internal/round/live.go`** (the tree message's node type and path resolution)

```go
// Package round plans one host round: for every union the profile belongs
// to, which records to merge, which ops to send and what to write. No I/O.
package round

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/chriscz/mirrormark/internal/db"
)

// TreeNode is one bookmark or folder in the extension's tree message.
// Top-level folders have Parent nil and Root set to toolbar, other or mobile.
type TreeNode struct {
	ID      string  `json:"id"`
	Parent  *string `json:"parent"`
	Index   int     `json:"index"`
	Title   string  `json:"title"`
	URL     string  `json:"url,omitempty"`
	Root    string  `json:"root,omitempty"`
	Syncing bool    `json:"syncing,omitempty"`
}

// Live is the browser's whole bookmarks tree.
type Live struct {
	top  []*liveNode
	byID map[string]*liveNode
}

type liveNode struct {
	TreeNode
	kids []*liveNode
}

func Build(nodes []TreeNode) (*Live, error) {
	l := &Live{byID: map[string]*liveNode{}}
	for _, n := range nodes {
		if _, dup := l.byID[n.ID]; dup {
			return nil, fmt.Errorf("tree: duplicate id %q", n.ID)
		}
		l.byID[n.ID] = &liveNode{TreeNode: n}
	}
	for _, n := range nodes {
		ln := l.byID[n.ID]
		if n.Parent == nil {
			l.top = append(l.top, ln)
			continue
		}
		p, ok := l.byID[*n.Parent]
		if !ok {
			return nil, fmt.Errorf("tree: node %q has unknown parent %q", n.ID, *n.Parent)
		}
		p.kids = append(p.kids, ln)
	}
	for _, n := range l.byID {
		slices.SortStableFunc(n.kids, func(a, b *liveNode) int { return cmp.Compare(a.Index, b.Index) })
	}
	return l, nil
}

// resolve follows segs and returns the deepest folder reached and how many
// segments matched. The first segment names a top-level folder by keyword
// or title; account folders (syncing) win over local ones of the same name.
func (l *Live) resolve(segs []string) (*liveNode, int) {
	var cur *liveNode
	for i, s := range segs {
		var next *liveNode
		if i == 0 {
			for _, t := range l.top {
				if (t.Root == s || t.Title == s) && (next == nil || t.Syncing && !next.Syncing) {
					next = t
				}
			}
		} else {
			for _, c := range cur.kids {
				if c.URL == "" && c.Title == s {
					next = c
					break
				}
			}
		}
		if next == nil {
			return cur, i
		}
		cur = next
	}
	return cur, len(segs)
}

func (n *liveNode) children() []*db.Node {
	var out []*db.Node
	for _, k := range n.kids {
		out = append(out, &db.Node{ID: k.ID, Title: k.Title, URL: k.URL, Children: k.children()})
	}
	return out
}
```

- [ ] **Step 2: Add `Tree` to the fake browser**

Append to `internal/fakebrowser/fakebrowser.go` (and import `github.com/chriscz/mirrormark/internal/round`):

```go
// Tree returns the whole tree as the extension sends it.
func (b *Browser) Tree() []round.TreeNode {
	roots := map[string]string{"1": "toolbar", "2": "other", "3": "mobile"}
	var out []round.TreeNode
	var walk func(n *node, parent *string)
	walk = func(n *node, parent *string) {
		for i, k := range n.kids {
			out = append(out, round.TreeNode{ID: k.id, Parent: parent, Index: i, Title: k.title, URL: k.url, Root: roots[k.id]})
			walk(k, new(k.id))
		}
	}
	walk(b.nodes["0"], nil)
	return out
}
```

Top-level nodes get `Parent: nil` (the walk starts below the invisible root `"0"`), and only they can have a `Root` keyword because ids 1–3 are the top-level folders.

- [ ] **Step 3: Write the failing unit tests** `internal/round/round_test.go`

```go
package round_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

func profile(id string) *db.DB {
	return &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: "chrome/" + id, Browser: "chrome"}}
}

func config(paths map[string]string) *db.Config {
	return &db.Config{Format: 1, Unions: map[string]map[string]string{"work": paths}}
}

func live(t *testing.T, b *fakebrowser.Browser) *round.Live {
	t.Helper()
	l, err := round.Build(b.Tree())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// apply runs a plan against b and returns the created ids by ref.
func apply(t *testing.T, b *fakebrowser.Browser, p round.Plan) map[string]string {
	t.Helper()
	created := map[string]string{}
	for i, r := range b.Apply(p.Ops, nil) {
		if !r.OK {
			t.Fatalf("op %+v: %s", p.Ops[i], r.Error)
		}
		if p.Ops[i].Op == "create" {
			created[p.Ops[i].Ref] = r.ID
		}
	}
	return created
}

func TestMissingMemberFolderIsCreatedFirst(t *testing.T) {
	b := fakebrowser.New()
	a := profile("a")
	cfg := config(map[string]string{"a": "toolbar/Work/Sub"})
	p := round.Make(round.Input{Config: cfg, Self: a, Live: live(t, b), NewM: merge.NewM})
	if !p.EnsureOnly || len(p.Ops) != 2 {
		t.Fatalf("plan %+v, want two folder creates", p)
	}
	apply(t, b, p)
	if got := b.Lines("1"); !slices.Equal(got, []string{"/Work/", "/Work/Sub/"}) {
		t.Fatalf("bar %q", got)
	}
}

func TestFirstRoundRecordsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	cfg := config(map[string]string{"a": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: a, Live: live(t, b), NewM: merge.NewM})
	next, changed, err := p.Finish(apply(t, b, p))
	if err != nil || !changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	rec := next.Unions["work"]
	if rec.Seq != 1 || rec.Path != "toolbar/Work" || rec.ID != w || len(rec.Children) != 1 || rec.Children[0].M == "" {
		t.Fatalf("record %+v", rec)
	}
	// A second round with nothing new writes nothing.
	p = round.Make(round.Input{Config: cfg, Self: next, Live: live(t, b), NewM: merge.NewM})
	if _, changed, _ := p.Finish(apply(t, b, p)); changed || len(p.Ops) != 0 {
		t.Fatalf("quiet round changed %v ops %+v", changed, p.Ops)
	}
}

func TestUnreadableMemberSkipsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	cfg := config(map[string]string{"a": "toolbar/Work", "b": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: profile("a"), Others: map[string]*db.DB{}, Live: live(t, b), NewM: merge.NewM})
	if !strings.Contains(p.Skipped["work"], "b") || len(p.Ops) != 0 {
		t.Fatalf("plan %+v", p)
	}
}

func TestReplacedMemberFolderIsSkipped(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	a := profile("a")
	a.Unions = map[string]*db.Member{"work": {Seq: 4, Path: "toolbar/Work", ID: "999"}}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if p.Skipped["work"] == "" {
		t.Fatalf("plan %+v, want work skipped", p)
	}
	next, changed, _ := p.Finish(nil)
	if changed || next.Unions["work"].Seq != 4 {
		t.Fatalf("skipped union must keep its record: changed %v %+v", changed, next.Unions["work"])
	}
}

func TestChangedConfigPathMakesANewMember(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("2", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	a.Unions = map[string]*db.Member{"work": {Seq: 4, Path: "toolbar/Work", ID: "999",
		Children: []*db.Node{{Title: "Old", URL: "https://old/", ID: "998", M: "m-old"}}}}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "other/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	next, changed, err := p.Finish(apply(t, b, p))
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	// No base: the old record is not read as deletions, and theirs is the
	// old record (highest seq), so its bookmark joins the new folder.
	if got := b.Lines(w); !slices.Equal(got, []string{"/GitHub https://github.com/", "/Old https://old/"}) {
		t.Fatalf("folder %q", got)
	}
	if next.Unions["work"].Seq != 5 {
		t.Fatalf("seq %d, want 5", next.Unions["work"].Seq)
	}
}

func TestTheirsIsTheHighestSeqRecord(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	a, x, y := profile("a"), profile("x"), profile("y")
	x.Unions = map[string]*db.Member{"work": {Seq: 7, Path: "toolbar/Work", Children: []*db.Node{{Title: "New", URL: "https://new/", M: "m-1"}}}}
	y.Unions = map[string]*db.Member{"work": {Seq: 3, Path: "toolbar/Work", Children: []*db.Node{{Title: "Stale", URL: "https://stale/", M: "m-2"}}}}
	cfg := config(map[string]string{"a": "toolbar/Work", "x": "toolbar/Work", "y": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: a, Others: map[string]*db.DB{"x": x, "y": y}, Live: live(t, b), NewM: merge.NewM})
	next, _, _ := p.Finish(apply(t, b, p))
	if got := b.Lines(w); !slices.Equal(got, []string{"/New https://new/"}) {
		t.Fatalf("folder %q", got)
	}
	if next.Unions["work"].Seq != 8 {
		t.Fatalf("seq %d, want 8", next.Unions["work"].Seq)
	}
}

func TestFinishFailsWithoutCreatedIDs(t *testing.T) {
	b := fakebrowser.New()
	b.Add("1", "Work", "")
	x := profile("x")
	x.Unions = map[string]*db.Member{"work": {Seq: 1, Path: "toolbar/Work", Children: []*db.Node{{Title: "N", URL: "https://n/", M: "m-1"}}}}
	cfg := config(map[string]string{"a": "toolbar/Work", "x": "toolbar/Work"})
	p := round.Make(round.Input{Config: cfg, Self: profile("a"), Others: map[string]*db.DB{"x": x}, Live: live(t, b), NewM: merge.NewM})
	if _, _, err := p.Finish(map[string]string{}); err == nil {
		t.Fatal("Finish accepted a create without an id")
	}
}

func TestFailedMergeSkipsTheUnion(t *testing.T) {
	defer round.SetMerge(func(merge.Input) (merge.Result, error) {
		return merge.Result{}, errors.New("merge: 1 nodes do not reach the member folder")
	})()
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	b.Add(w, "GitHub", "https://github.com/")
	a := profile("a")
	old := &db.Member{Seq: 4, Path: "toolbar/Work", ID: w, Children: []*db.Node{{Title: "GitHub", URL: "https://github.com/", ID: "5", M: "m-1"}}}
	a.Unions = map[string]*db.Member{"work": old}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if !strings.Contains(p.Skipped["work"], "do not reach") || len(p.Ops) != 0 || len(p.WrongTree) != 0 {
		t.Fatalf("plan %+v", p)
	}
	next, changed, err := p.Finish(nil)
	if err != nil || changed || next.Unions["work"] != old {
		t.Fatalf("a failed merge must keep the record: changed %v err %v %+v", changed, err, next.Unions["work"])
	}
}

func TestWrongTreeSkipsTheUnion(t *testing.T) {
	b := fakebrowser.New()
	w := b.Add("1", "Work", "")
	a := profile("a")
	old := &db.Member{Seq: 4, Path: "toolbar/Work", ID: w}
	for i := range 6 {
		old.Children = append(old.Children, &db.Node{Title: "B", URL: fmt.Sprintf("https://b/%d", i), ID: fmt.Sprint(100 + i), M: fmt.Sprintf("m-%d", i)})
	}
	a.Unions = map[string]*db.Member{"work": old}
	p := round.Make(round.Input{Config: config(map[string]string{"a": "toolbar/Work"}), Self: a, Live: live(t, b), NewM: merge.NewM})
	if !slices.Equal(p.WrongTree, []string{"work"}) || p.Skipped["work"] == "" || len(p.Ops) != 0 {
		t.Fatalf("plan %+v", p)
	}
	if next, changed, _ := p.Finish(nil); changed || next.Unions["work"] != old {
		t.Fatalf("the guard must keep the record: changed %v", changed)
	}
}
```

- [ ] **Step 4: Run them to see them fail**

Run: `bin/dev run go test ./internal/round/`
Expected: FAIL, `undefined: round.Make`.

- [ ] **Step 5: Write `internal/round/round.go`**

```go
package round

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/merge"
)

// mergeRound is merge.Round; a test replaces it to make a merge fail.
var mergeRound = merge.Round

type Input struct {
	Config *db.Config
	Self   *db.DB
	Others map[string]*db.DB // other members' files by profile id; absent = unreadable
	Live   *Live
	NewM   func() string
}

// Plan is what one round should do. If EnsureOnly, Ops only create missing
// member folders and the round must not write: the next tree shows them.
type Plan struct {
	Ops        []merge.Op
	EnsureOnly bool
	Conflicts  []merge.Conflict
	Skipped    map[string]string // union → why it was not synced
	WrongTree  []string          // unions the wrong-tree guard refused

	self *db.DB
	next *db.DB
	seq  map[string]int64
}

func Make(in Input) Plan {
	p := Plan{Skipped: map[string]string{}, self: in.Self, seq: map[string]int64{}}
	next := *in.Self
	next.Unions = map[string]*db.Member{}
	var ensure []merge.Op
	me := in.Self.Profile.ID
	for _, name := range slices.Sorted(maps.Keys(in.Config.Unions)) {
		members := in.Config.Unions[name]
		path, ok := members[me]
		if !ok {
			continue
		}
		skip := func(reason string) {
			p.Skipped[name] = reason
			if old := in.Self.Unions[name]; old != nil {
				next.Unions[name] = old
			}
		}
		// A record made for another path is not a base: the config points
		// somewhere new, so this is a new member. It still counts as a
		// published state of the union, so it can be theirs.
		var records []*db.Member
		rec := in.Self.Unions[name]
		if rec != nil {
			records = append(records, rec)
			if rec.Path != path {
				rec = nil
			}
		}
		unreadable := ""
		for _, id := range slices.Sorted(maps.Keys(members)) {
			if id == me {
				continue
			}
			o, ok := in.Others[id]
			if !ok {
				unreadable = id
				break
			}
			if r := o.Unions[name]; r != nil {
				records = append(records, r)
			}
		}
		if unreadable != "" {
			// Never treat a member we cannot read as empty.
			skip(fmt.Sprintf("cannot read the database file of member %s", unreadable))
			continue
		}
		segs, err := db.SplitPath(path)
		if err != nil {
			skip(err.Error())
			continue
		}
		folder, n := in.Live.resolve(segs)
		if n < len(segs) {
			if rec != nil {
				// A folder that disappeared never means "everything was deleted".
				skip(fmt.Sprintf("member folder %s is missing; restore it or change the path in config.yaml", path))
				continue
			}
			if folder == nil {
				skip(fmt.Sprintf("no top-level folder %q", segs[0]))
				continue
			}
			parent := folder.ID
			for i := n; i < len(segs); i++ {
				ref := fmt.Sprintf("path:%s:%d", name, i)
				ensure = append(ensure, merge.Op{Op: "create", Ref: ref, Parent: parent, Title: new(segs[i])})
				parent = ref
			}
			continue
		}
		if rec != nil && rec.ID != folder.ID {
			skip(fmt.Sprintf("%s is a different folder than before; restore it or change the path in config.yaml", path))
			continue
		}
		var theirs *db.Member
		var top int64
		for _, r := range records {
			if theirs == nil || r.Seq > theirs.Seq {
				theirs = r
			}
			top = max(top, r.Seq)
		}
		mi := merge.Input{HasBase: rec != nil, Ours: folder.children(), RootID: folder.ID, Label: in.Self.Profile.Label, NewM: in.NewM}
		if rec != nil {
			mi.Base = rec.Children
		}
		if theirs != nil {
			mi.Theirs = theirs.Children
		}
		res, err := mergeRound(mi)
		if err != nil {
			// Never go on with an empty result: it would publish an
			// empty union to every member.
			if errors.Is(err, merge.ErrWrongTree) {
				p.WrongTree = append(p.WrongTree, name)
			}
			skip(err.Error())
			continue
		}
		p.Ops = append(p.Ops, res.Ops...)
		p.Conflicts = append(p.Conflicts, res.Conflicts...)
		p.seq[name] = top + 1
		next.Unions[name] = &db.Member{Path: path, ID: folder.ID, Children: res.Tree}
	}
	if len(ensure) > 0 {
		p.Ops, p.EnsureOnly = ensure, true
	}
	p.next = &next
	return p
}

// Finish fills in the ids of created nodes and reports whether the database
// file changes. Only changed union records get a new seq, so a round that
// changes nothing writes nothing and the rounds stop.
func (p Plan) Finish(created map[string]string) (*db.DB, bool, error) {
	changed := false
	for name := range p.self.Unions {
		if _, ok := p.next.Unions[name]; !ok {
			changed = true
		}
	}
	for name, rec := range p.next.Unions {
		old := p.self.Unions[name]
		if rec == old {
			continue // skipped: kept as it was
		}
		if err := fillIDs(rec.Children, created); err != nil {
			return nil, false, err
		}
		if old != nil && old.Path == rec.Path && old.ID == rec.ID && db.EqualNodes(old.Children, rec.Children) {
			rec.Seq = old.Seq
			continue
		}
		rec.Seq = p.seq[name]
		changed = true
	}
	return p.next, changed, nil
}

func fillIDs(ns []*db.Node, created map[string]string) error {
	for _, n := range ns {
		if n.ID == "" {
			id, ok := created[n.M]
			if !ok {
				return fmt.Errorf("no browser id for created node %s %q", n.M, n.Title)
			}
			n.ID = id
		}
		if err := fillIDs(n.Children, created); err != nil {
			return err
		}
	}
	return nil
}
```

`internal/round/export_test.go` lets the tests force a failed merge:

```go
package round

import "github.com/chriscz/mirrormark/internal/merge"

// SetMerge replaces merge.Round for one test and returns the undo.
func SetMerge(f func(merge.Input) (merge.Result, error)) func() {
	old := mergeRound
	mergeRound = f
	return func() { mergeRound = old }
}
```

- [ ] **Step 6: Run the unit tests**

Run: `bin/dev run go test ./internal/round/ -run 'Test[^P]' -v`
Expected: PASS.

- [ ] **Step 7: Write the property test** `internal/round/property_test.go`

Three members, one union at `toolbar/U`, each starting with its own bookmarks (30 nodes in all). The test implements `fakebrowser.Union` over `round.Make` and `Finish`, each member's file going through YAML as on disk, and runs it through `fakebrowser.Fuzz`, the driver the merge fuzz test already used: bursts of random user edits, purges included; sweeps in which members are closed and batches fail part-way; then sweeps until one neither writes nor sends ops. After each burst every member's union must be identical (ignoring sibling order and browser ids) and no URL may be lost unless a user purged or changed it. Removes are capped at 4 nodes per burst, so the wrong-tree guard has no cause to trip and any refusal fails the test. The fuzz editor adds colliding URLs on purpose, so the test does not check that each URL appears once.

```go
package round_test

import (
	"fmt"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
	"go.yaml.in/yaml/v3"
)

type member struct {
	b    *fakebrowser.Browser
	root string
	file *db.DB
}

// hosts is a union at toolbar/U whose rounds go through round.Make and
// Finish as the host's would, each database file through YAML as on disk.
type hosts struct {
	cfg *db.Config
	ms  []*member
}

func newHosts() *hosts {
	h := &hosts{cfg: config(map[string]string{})}
	for i := range 3 {
		id := fmt.Sprintf("p%d", i)
		h.cfg.Unions["work"][id] = "toolbar/U"
		b := fakebrowser.New()
		m := &member{b: b, root: b.Add("1", "U", ""), file: profile(id)}
		// Each member starts with its own bookmarks, and all with one the
		// same; the first rounds join them. With 30 nodes in the union and
		// few removes per burst, the wrong-tree guard has no reason to trip.
		n := 8
		if i == 0 {
			n = 12
		}
		for j := range n {
			b.Add(m.root, fmt.Sprintf("I%d", j), fmt.Sprintf("https://init/%s/%d", id, j))
		}
		b.Add(b.Add(m.root, "Shared", ""), "Same", "https://same/")
		h.ms = append(h.ms, m)
	}
	return h
}

func (h *hosts) Len() int { return len(h.ms) }

func (h *hosts) Member(i int) (*fakebrowser.Browser, string, []*db.Node) {
	m := h.ms[i]
	var rec []*db.Node
	if r := m.file.Unions["work"]; r != nil {
		rec = r.Children
	}
	return m.b, m.root, rec
}

func (h *hosts) Edited(int) {}

// Round reports a round that sent ops as a change too, so a quiet sweep is
// one that neither writes nor touches a browser.
func (h *hosts) Round(i int, fail func(int) bool) (bool, error) {
	m := h.ms[i]
	others := map[string]*db.DB{}
	for _, o := range h.ms {
		if o != m {
			others[o.file.Profile.ID] = o.file
		}
	}
	l, err := round.Build(m.b.Tree())
	if err != nil {
		return false, err
	}
	p := round.Make(round.Input{Config: h.cfg, Self: m.file, Others: others, Live: l, NewM: merge.NewM})
	if len(p.WrongTree) > 0 {
		return false, fmt.Errorf("member %d: %w", i, merge.ErrWrongTree)
	}
	if len(p.Skipped) > 0 {
		return false, fmt.Errorf("member %d: skipped %v", i, p.Skipped)
	}
	created := map[string]string{}
	for j, r := range m.b.Apply(p.Ops, fail) {
		if r.Error == "injected" {
			return false, nil // the batch stopped here; no file is written
		}
		if !r.OK {
			return false, fmt.Errorf("member %d: op %+v: %s", i, p.Ops[j], r.Error)
		}
		if p.Ops[j].Op == "create" {
			created[p.Ops[j].Ref] = r.ID
		}
	}
	if p.EnsureOnly {
		return false, fmt.Errorf("member %d: member folder missing", i)
	}
	next, changed, err := p.Finish(created)
	if err != nil {
		return false, fmt.Errorf("member %d: %w", i, err)
	}
	if changed {
		data, err := yaml.Marshal(next)
		if err != nil {
			return false, err
		}
		var d db.DB
		if err := yaml.Unmarshal(data, &d); err != nil {
			return false, err
		}
		m.file = &d
	}
	return changed || len(p.Ops) > 0, nil
}

// TestConvergence runs three hosts through random edits, closed profiles
// and batches that fail part-way, with removes kept under the wrong-tree
// guard. Run more seeds with -mirrormark.fuzz=20000.
func TestConvergence(t *testing.T) {
	failed := 0
	for seed := range fakebrowser.Seeds() {
		if err := fakebrowser.Fuzz(uint64(seed), newHosts(), 4); err != nil {
			t.Errorf("seed %d: %v", seed, err)
			if failed++; failed > 10 {
				t.Fatal("too many failing seeds")
			}
		}
	}
}
```

- [ ] **Step 8: Run the property test**

Run: `bin/dev run go test ./internal/round/ -run TestConvergence -count=1`
Expected: PASS for all 300 seeds (`-args -mirrormark.fuzz=20000` runs more). A failing seed prints its number; fix the cause in `merge` or `round` (add a unit test in `merge_test.go` that reproduces it first). Do not weaken an invariant to make a seed pass; if an invariant looks wrong, stop and raise it.

- [ ] **Step 9: Verify**

Run: `bin/dev run make`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/round internal/fakebrowser
git commit -m "feat(rfc-0001): plan sync rounds across member database files [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Native messaging host

**Files:**
- Create: `internal/host/protocol.go`, `internal/host/host.go`
- Test: `internal/host/host_test.go`
- Modify: `cmd/mirrormark/main.go`, `README.md`

**Interfaces:**
- Consumes: `db.*` (paths, `LoadConfig`, `LoadDB`, `SaveDB`, `Backup`, `Lock`, `TryLock`, `ErrLocked`, `Connected`, `ValidID`); `round.Build`, `round.Make`, `Plan.Finish`, `round.TreeNode`; `merge.Op`, `merge.OpResult`, `merge.NewM`; `fakebrowser` in tests.
- Produces: `host.Msg`, `host.ReadMsg`, `host.WriteMsg`, `host.Options`, `host.Run`; the `host` subcommand and host-mode detection (Chrome runs the binary with `chrome-extension://<id>/` as its first argument). The host touches `profiles/<id>.lock` on every `hello` (task 7's `install` waits for that).

**Protocol** (JSON, 32-bit native-endian length prefix, as Chrome requires):

| Direction | `type` | Fields |
| --- | --- | --- |
| ext → host | `hello` | `v`, `db`, `browser` |
| ext → host | `tree` | `v`, `nodes` (`round.TreeNode`) |
| host → ext | `apply` | `v`, `batch`, `ops` (≤ 500) |
| ext → host | `applied` | `v`, `batch`, `results` (one per op) |
| either | `error` | `v`, `code` (`unsupported-version`, `bad-db`, `db-missing`, `db-locked`, `config-invalid`, `db-mismatch`, `union-skipped`), `message` |

- [ ] **Step 1: Write `internal/host/protocol.go`**

```go
// Package host is the native messaging host: Chrome starts it when the
// extension connects, and it runs sync rounds for that one profile.
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
	if err := binary.Write(w, binary.NativeEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
```

- [ ] **Step 2: Write the failing tests** `internal/host/host_test.go`

A fake extension drives a real `host.Run` over pipes with a `fakebrowser.Browser`. Two of them share one set of directories, as two Chrome profiles would.

```go
package host_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/host"
)

type ext struct {
	t      *testing.T
	mu     sync.Mutex
	b      *fakebrowser.Browser
	w      *io.PipeWriter
	errs   chan host.Msg
	done   chan error
	silent bool
}

func paths(t *testing.T) db.Paths {
	d := t.TempDir()
	return db.Paths{Config: d + "/config", Data: d + "/data", State: d + "/state"}
}

func addProfile(t *testing.T, p db.Paths, id string) {
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: "chrome/" + id, Browser: "chrome"}}
	if err := db.SaveDB(p.ProfileFile(id), d); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, p db.Paths, text string) {
	if err := db.WriteAtomic(p.ConfigFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// start connects a fake extension bound to dbID and sends hello and a tree.
// A silent extension never answers apply.
func start(t *testing.T, p db.Paths, dbID string, b *fakebrowser.Browser, silent bool) *ext {
	hr, ew := io.Pipe()
	er, hw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	e := &ext{t: t, b: b, w: ew, errs: make(chan host.Msg, 10), done: make(chan error, 1), silent: silent}
	go func() {
		e.done <- host.Run(ctx, hr, hw, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Timeout: 300 * time.Millisecond, Poll: 20 * time.Millisecond})
		hw.Close()
	}()
	t.Cleanup(func() { cancel(); ew.Close() })
	go func() {
		for {
			m, err := host.ReadMsg(er)
			if err != nil {
				return
			}
			switch m.Type {
			case "apply":
				if e.silent {
					continue
				}
				e.mu.Lock()
				res := e.b.Apply(m.Ops, nil)
				e.mu.Unlock()
				e.send(host.Msg{V: 1, Type: "applied", Batch: m.Batch, Results: res})
				e.sendTree()
			case "error":
				select {
				case e.errs <- m:
				default: // warnings can repeat every round; keep the first few
				}
			}
		}
	}()
	e.send(host.Msg{V: 1, Type: "hello", DB: dbID, Browser: "chrome/test"})
	e.sendTree()
	return e
}

func (e *ext) send(m host.Msg) {
	if err := host.WriteMsg(e.w, m); err != nil {
		e.t.Log("send:", err)
	}
}

func (e *ext) sendTree() {
	e.mu.Lock()
	nodes := e.b.Tree()
	e.mu.Unlock()
	e.send(host.Msg{V: 1, Type: "tree", Nodes: nodes})
}

// edit changes the browser as a user would, then reports the new tree.
func (e *ext) edit(f func(b *fakebrowser.Browser)) {
	e.mu.Lock()
	f(e.b)
	e.mu.Unlock()
	e.sendTree()
}

func (e *ext) lines(folder string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.b.Find("1", folder)
	if id == "" {
		return nil
	}
	return e.b.Lines(id)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTwoHostsSyncAUnion(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n    b: toolbar/Work\n")
	a := start(t, p, "a", fakebrowser.New(), false)
	b := start(t, p, "b", fakebrowser.New(), false)
	eventually(t, "member folders", func() bool { return a.lines("Work") != nil && b.lines("Work") != nil })

	a.edit(func(br *fakebrowser.Browser) { br.Add(br.Find("1", "Work"), "GitHub", "https://github.com/") })
	want := []string{"/GitHub https://github.com/"}
	eventually(t, "add to reach b", func() bool { return slices.Equal(b.lines("Work"), want) })

	b.edit(func(br *fakebrowser.Browser) { br.Remove(br.Find(br.Find("1", "Work"), "GitHub")) })
	want = []string{"/_deleted/", "/_deleted/GitHub https://github.com/"}
	eventually(t, "delete to reach a", func() bool { return slices.Equal(a.lines("Work"), want) })

	// Quiet: no more writes.
	time.Sleep(200 * time.Millisecond)
	before, _ := os.Stat(p.ProfileFile("a"))
	time.Sleep(300 * time.Millisecond)
	after, _ := os.Stat(p.ProfileFile("a"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("a's database file is still being written")
	}
	if !db.Connected(p, "a", time.Time{}) {
		t.Fatal("a's hello did not touch its lock file")
	}
}

func TestUnknownDatabaseIsRefused(t *testing.T) {
	e := start(t, paths(t), "nope", fakebrowser.New(), false)
	if m := <-e.errs; m.Code != "db-missing" {
		t.Fatalf("error %+v", m)
	}
	if err := <-e.done; err == nil {
		t.Fatal("host kept running")
	}
}

func TestSecondHostForOneDatabaseIsRefused(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	start(t, p, "a", fakebrowser.New(), false)
	time.Sleep(50 * time.Millisecond)
	e := start(t, p, "a", fakebrowser.New(), false)
	if m := <-e.errs; m.Code != "db-locked" {
		t.Fatalf("error %+v", m)
	}
}

func TestOtherVersionIsRefused(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	hr, ew := io.Pipe()
	er, hw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), hr, hw, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}) }()
	go host.WriteMsg(ew, host.Msg{V: 2, Type: "hello", DB: "a"})
	m, err := host.ReadMsg(er)
	if err != nil || m.Code != "unsupported-version" {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestSilentBrowserWritesNothing(t *testing.T) {
	p := paths(t)
	addProfile(t, p, "a")
	addProfile(t, p, "b")
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    a: toolbar/Work\n    b: toolbar/Work\n")
	bb := fakebrowser.New()
	bb.Add(bb.Add("1", "Work", ""), "X", "https://x/")
	start(t, p, "b", bb, false)
	eventually(t, "b's first write", func() bool {
		d, err := db.LoadDB(p.ProfileFile("b"))
		return err == nil && d.Unions["work"] != nil
	})
	ab := fakebrowser.New()
	ab.Add("1", "Work", "")
	start(t, p, "a", ab, true)
	time.Sleep(800 * time.Millisecond) // several rounds, each timing out after 300 ms
	d, err := db.LoadDB(p.ProfileFile("a"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Unions["work"] != nil {
		t.Fatalf("a wrote a record without its browser applying the batch: %+v", d.Unions["work"])
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `bin/dev run go test ./internal/host/`
Expected: FAIL, `undefined: host.Run`.

- [ ] **Step 4: Write `internal/host/host.go`**

```go
package host

import (
	"context"
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

const batchSize = 500

// errStop ends Run after telling the extension why.
var errStop = errors.New("host stopped")

type state struct {
	o      Options
	msgs   <-chan Msg
	out    io.Writer
	self   string
	live   *round.Live
	fresh  bool // live reflects the browser after our last batch
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
			if s.fresh && s.filesChanged() {
				s.round()
			}
		}
	}
}

func (s *state) send(m Msg) {
	m.V = Version
	if err := WriteMsg(s.out, m); err != nil {
		s.o.Log.Warn("send failed", "err", err)
	}
}

func (s *state) fail(code, msg string) error {
	s.o.Log.Warn(msg, "code", code)
	s.send(Msg{Type: "error", Code: code, Message: msg})
	return fmt.Errorf("%w: %s", errStop, msg)
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
		s.round()
	case "applied":
		s.o.Log.Info("late applied message ignored", "batch", m.Batch)
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
	// Held until the process exits: a copied profile bound to the same
	// file must not drive it twice.
	lock := s.o.Paths.ProfileLock(m.DB)
	if _, err := db.TryLock(lock); err != nil {
		if errors.Is(err, db.ErrLocked) {
			return s.fail("db-locked", fmt.Sprintf("another browser profile is using %s; was this profile copied?", m.DB))
		}
		return s.fail("db-locked", err.Error())
	}
	// install and the tests watch this mtime to see that the profile connected.
	now := time.Now()
	if err := os.Chtimes(lock, now, now); err != nil {
		s.o.Log.Warn("cannot touch lock file", "err", err)
	}
	s.self = m.DB
	s.o.Log.Info("connected", "db", m.DB, "browser", m.Browser)
	return nil
}

// warn logs a problem and shows it in the extension's popup.
func (s *state) warn(code, msg string) {
	s.o.Log.Warn(msg, "code", code)
	s.send(Msg{Type: "error", Code: code, Message: msg})
}

// filesChanged reports whether config.yaml or any database file changed
// since this host last looked. Polling a few files once a second costs
// nothing measurable and needs no inotify.
func (s *state) filesChanged() bool {
	now := s.stat()
	return !maps.Equal(now, s.mtimes)
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

// round runs one sync round under the global lock. If anything fails it
// writes no file, so the next round starts again from the same base.
func (s *state) round() {
	unlock, err := db.Lock(s.o.Paths.GlobalLock())
	if err != nil {
		s.o.Log.Warn("cannot lock", "err", err)
		return
	}
	defer unlock()
	defer func() { s.mtimes = s.stat() }() // also covers our own write

	cfg, err := db.LoadConfig(s.o.Paths.ConfigFile())
	if err != nil {
		s.warn("config-invalid", err.Error())
		return
	}
	self, err := db.LoadDB(s.o.Paths.ProfileFile(s.self))
	if err != nil {
		s.warn("db-missing", err.Error())
		return
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
		s.warn("union-skipped", fmt.Sprintf("not syncing %s: %s", u, plan.Skipped[u]))
	}
	for _, c := range plan.Conflicts {
		s.o.Log.Info("conflict", "folder", c.Folder, "ours", c.Ours, "theirs", c.Theirs)
	}
	if len(plan.WrongTree) > 0 {
		s.warn("db-mismatch", fmt.Sprintf("not syncing %v: %v", plan.WrongTree, merge.ErrWrongTree))
	}
	created := map[string]string{}
	if len(plan.Ops) > 0 {
		s.fresh = false // the tree is stale once we write to the browser
		for chunk := range slices.Chunk(plan.Ops, batchSize) {
			s.batch++
			s.send(Msg{Type: "apply", Batch: s.batch, Ops: chunk})
			results, err := s.await(s.batch)
			if err == nil && len(results) != len(chunk) {
				err = fmt.Errorf("browser answered %d of %d ops", len(results), len(chunk))
			}
			if err != nil {
				s.o.Log.Warn("batch failed; nothing written", "err", err)
				return
			}
			failed := 0
			for i, r := range results {
				switch {
				case !r.OK:
					failed++
					s.o.Log.Warn("op failed", "op", chunk[i].Op, "id", chunk[i].ID, "err", r.Error)
				case chunk[i].Op == "create":
					created[chunk[i].Ref] = r.ID
				}
			}
			if failed > 0 {
				s.o.Log.Warn("changes failed in the browser; nothing written, will retry", "failed", failed)
				return
			}
		}
	}
	if plan.EnsureOnly {
		return
	}
	next, changed, err := plan.Finish(created)
	if err != nil {
		s.o.Log.Warn("cannot finish round", "err", err)
		return
	}
	if !changed {
		return
	}
	if err := db.Backup(s.o.Paths, s.self, time.Now()); err != nil {
		s.o.Log.Warn("backup failed", "err", err)
	}
	if err := db.SaveDB(s.o.Paths.ProfileFile(s.self), next); err != nil {
		s.o.Log.Warn("cannot write database", "err", err)
	}
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
			}
		case <-t.C:
			return nil, fmt.Errorf("the browser did not apply a batch within %v", s.o.Timeout)
		}
	}
}
```

Note on `fresh`: a tree that arrives while waiting is stored but leaves `fresh` false; the extension sends one more tree after every batch, which sets it back. If `await` returns `io.EOF`, the next `<-msgs` in `Run` sees the closed channel and returns.

- [ ] **Step 5: Run the host tests**

Run: `bin/dev run go test ./internal/host/ -v -count=1`
Expected: PASS.

- [ ] **Step 6: Wire up host mode in `main.go`**

In `cmd/mirrormark/main.go`, add host mode. Chrome runs the binary named in the host manifest with the extension's origin as its only argument, so that argument selects host mode:

```go
const usageText = `Usage: mirrormark <command> [options]

Commands:
  host        Run as the native messaging host (Chrome does this)
  help        Show this help
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	if strings.HasPrefix(args[0], "chrome-extension://") || args[0] == "host" {
		return cmdHost(stdin, stdout, stderr)
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "mirrormark: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

// cmdHost logs to the log file: stdout belongs to the protocol.
func cmdHost(stdin io.Reader, stdout, stderr io.Writer) int {
	p, err := db.DefaultPaths()
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	f, err := os.OpenFile(p.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	defer f.Close()
	log := slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	if err := host.Run(context.Background(), stdin, stdout, host.Options{Paths: p, Log: log}); err != nil {
		log.Warn("host stopped", "err", err)
		return 1
	}
	return 0
}
```

(Add imports `context`, `log/slog`, `strings`, and the `db` and `host` packages.)

- [ ] **Step 7: Run all tests**

Run: `bin/dev run make`
Expected: PASS.

- [ ] **Step 8: Docs**

README, before "Development":

```markdown
## When something is wrong

The extension's popup (the mirrormark button in Chrome's toolbar) shows
which database the profile syncs as and the last error. Everything the
host does, including skipped unions, conflicts and failed changes, goes to
`~/.local/state/mirrormark/log`.
```

- [ ] **Step 9: Commit**

```bash
git add internal/host cmd/mirrormark README.md
git commit -m "feat(rfc-0001): sync through a native messaging host [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Embedded extension

Plain JavaScript, no build step: the binary embeds these files as they are. The extension's behaviour is tested end to end in tasks 8 and 9; here Go tests check the embedded files and `Unpack`. Before committing, load it by hand once (Step 8) so a syntax error does not wait for task 8.

**Files:**
- Create: `extension/embed.go`, `extension/manifest.json`, `extension/background.js`, `extension/bind.html`, `extension/bind.js`, `extension/popup.html`, `extension/popup.js`
- Test: `extension/embed_test.go`

**Interfaces:**
- Consumes: the protocol of task 5 (`hello`, `tree`, `apply`, `applied`, `error`; `round.TreeNode` fields; `merge.Op` fields).
- Produces: `extension.HostName`, `extension.Files`, `extension.Unpack(dir string, publicKeyDER []byte) error`, `extension.Version() (string, error)`; the pages `bind.html#<db>` and `popup.html` used by tasks 7–9; `chrome.storage.local` keys `db`, `boundAt`, `status`.

- [ ] **Step 1: Write the failing test** `extension/embed_test.go`

```go
package extension

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnpackWritesFilesAndKey(t *testing.T) {
	dir := t.TempDir()
	pub := []byte{1, 2, 3}
	if err := Unpack(dir, pub); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"manifest.json", "background.js", "bind.html", "bind.js", "popup.html", "popup.js"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	var m struct {
		Key         string   `json:"key"`
		Permissions []string `json:"permissions"`
		Version     string   `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Key != base64.StdEncoding.EncodeToString(pub) {
		t.Fatalf("key %q", m.Key)
	}
	if strings.Join(m.Permissions, ",") != "bookmarks,nativeMessaging,storage" {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if v, _ := Version(); v != m.Version || v == "" {
		t.Fatalf("Version() %q, manifest %q", v, m.Version)
	}
}

func TestBackgroundUsesHostName(t *testing.T) {
	js, _ := Files.ReadFile("background.js")
	if !strings.Contains(string(js), `const HOST = "`+HostName+`";`) {
		t.Fatal("background.js and HostName disagree")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `bin/dev run go test ./extension/`
Expected: FAIL, undefined names.

- [ ] **Step 3: Write `extension/manifest.json`**

```json
{
  "manifest_version": 3,
  "name": "mirrormark",
  "version": "0.1.0",
  "description": "Keeps bookmark folders the same across browser profiles through YAML files on this computer.",
  "minimum_chrome_version": "134",
  "permissions": ["bookmarks", "nativeMessaging", "storage"],
  "background": { "service_worker": "background.js" },
  "action": { "default_title": "mirrormark", "default_popup": "popup.html" }
}
```

`version` is the product version. Raise it for every release that changes the extension: Chrome installs a new CRX only when `external_version` (taken from here) changes.

- [ ] **Step 4: Write `extension/background.js`**

```js
// mirrormark: sends this profile's bookmark tree to the local mirrormark
// program and applies the changes it sends back. It stays idle until
// `mirrormark install` binds the profile to a database.
const HOST = "com.github.chriscz.mirrormark";
const V = 1;
const QUIET_MS = 500; // send a tree after this long without bookmark events
const ROOTS = { "bookmarks-bar": "toolbar", other: "other", mobile: "mobile" };

let port = null;
let connecting = null;
let quiet = null;
let retry = null;
let backoff = 5000;
let queue = Promise.resolve();
const refs = new Map(); // create ref -> new bookmark id, for later ops' parents

// Chrome starts the service worker for these events and the top-level
// connect() below runs. Without an onStartup listener the worker does not
// start when a profile opens.
chrome.runtime.onStartup.addListener(() => {});
chrome.runtime.onInstalled.addListener(() => {});
for (const e of [
  chrome.bookmarks.onCreated,
  chrome.bookmarks.onRemoved,
  chrome.bookmarks.onChanged,
  chrome.bookmarks.onMoved,
  chrome.bookmarks.onChildrenReordered,
  chrome.bookmarks.onImportEnded,
]) {
  e.addListener(changed);
}
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local" || !(changes.db || changes.boundAt)) return;
  // A new binding: reconnect so the host sees a fresh hello.
  if (port) port.disconnect();
  port = null;
  connect();
});
connect();

function changed() {
  if (!port) {
    connect();
    return;
  }
  clearTimeout(quiet);
  quiet = setTimeout(sendTree, QUIET_MS);
}

function connect() {
  connecting ??= (async () => {
    try {
      if (port) return;
      const { db } = await chrome.storage.local.get("db");
      if (!db) return;
      clearTimeout(retry);
      const p = chrome.runtime.connectNative(HOST);
      port = p;
      p.onMessage.addListener((m) => {
        queue = queue.then(() => onMessage(p, m));
      });
      p.onDisconnect.addListener(() => {
        const err = chrome.runtime.lastError?.message;
        if (port === p) port = null;
        if (err) setStatus({ error: err });
        retry = setTimeout(connect, backoff);
      });
      const version = navigator.userAgent.match(/Chrome\/([\d.]+)/)?.[1] ?? "";
      p.postMessage({ v: V, type: "hello", db, browser: "chrome/" + version });
      await sendTree();
    } finally {
      connecting = null;
    }
  })();
  return connecting;
}

async function sendTree() {
  const p = port;
  if (!p) return;
  const [root] = await chrome.bookmarks.getTree();
  const nodes = [];
  const walk = (n, parent) =>
    (n.children ?? []).forEach((c, index) => {
      nodes.push({
        id: c.id,
        parent,
        index,
        title: c.title,
        url: c.url,
        root: parent === null ? ROOTS[c.folderType] : undefined,
        syncing: c.syncing || undefined,
      });
      walk(c, c.id);
    });
  walk(root, null);
  p.postMessage({ v: V, type: "tree", nodes });
}

async function onMessage(p, m) {
  if (m.type === "apply") {
    const results = [];
    // One at a time, and none after a failure: each op assumes the earlier ones landed.
    for (const op of m.ops) results.push(results.some((r) => !r.ok) ? { ok: false, error: "not run" } : await applyOp(op));
    p.postMessage({ v: V, type: "applied", batch: m.batch, results });
    const failed = results.find((r) => !r.ok);
    await setStatus({ error: failed ? failed.error : null });
    backoff = 5000;
    await sendTree();
  } else if (m.type === "error") {
    // The host exits after most errors; don't restart it every 5 s.
    backoff = 60000;
    await setStatus({ error: `${m.code}: ${m.message}` });
  }
}

async function clamp(parentId, index) {
  if (index === undefined) return undefined;
  const kids = await chrome.bookmarks.getChildren(parentId);
  return Math.min(index, kids.length);
}

// applyOp runs one op. update, move and remove first check that the node is
// as the host expects, so an edit the user makes during a batch survives.
async function applyOp(op) {
  try {
    const parent = op.parent && (refs.get(op.parent) ?? op.parent);
    switch (op.op) {
      case "create": {
        const n = await chrome.bookmarks.create({
          parentId: parent,
          index: await clamp(parent, op.index),
          title: op.title,
          url: op.url,
        });
        refs.set(op.ref, n.id);
        return { ok: true, id: n.id };
      }
      case "update": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (n.title !== op.expect.title || (n.url ?? "") !== op.expect.url) return { ok: false, error: "stale" };
        const changes = {};
        if (op.title !== undefined) changes.title = op.title;
        if (op.url !== undefined) changes.url = op.url;
        await chrome.bookmarks.update(op.id, changes);
        return { ok: true };
      }
      case "move": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (n.parentId !== op.expect.parent) return { ok: false, error: "stale" };
        await chrome.bookmarks.move(op.id, { parentId: parent, index: await clamp(parent, op.index) });
        return { ok: true };
      }
      case "remove": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (n.title !== op.expect.title || (n.url ?? "") !== op.expect.url) return { ok: false, error: "stale" };
        // Never removeTree: the host removes a folder's contents first, so a
        // folder that is not empty holds something the round meant to keep.
        await chrome.bookmarks.remove(op.id);
        return { ok: true };
      }
    }
    return { ok: false, error: "unknown op " + op.op };
  } catch (e) {
    return { ok: false, error: String(e) };
  }
}

async function setStatus(s) {
  const { status } = await chrome.storage.local.get("status");
  await chrome.storage.local.set({ status: { ...status, ...s } });
}
```

The host always sends `expect.title` and `expect.url` for `update` and `remove` (`merge.ops`), so the checks above compare against both.

- [ ] **Step 5: Write the pages**

`extension/bind.html`:

```html
<!doctype html>
<meta charset="utf-8" />
<title>mirrormark</title>
<p id="msg">Binding…</p>
<script src="bind.js"></script>
```

`extension/bind.js`:

```js
// Opened by `mirrormark install` in one profile as bind.html#<database id>.
// The page is not web-accessible, so web pages cannot open it.
const db = decodeURIComponent(location.hash.slice(1));
const msg = document.getElementById("msg");
if (!/^[a-z0-9][a-z0-9-]*$/.test(db)) {
  msg.textContent = "This link does not name a mirrormark database.";
} else {
  // boundAt changes even when db does not, so a rebind always reconnects.
  chrome.storage.local.set({ db, boundAt: Date.now() }).then(() => {
    msg.textContent = `This profile now syncs as ${db}. You can close this tab.`;
    setTimeout(() => window.close(), 1500);
  });
}
```

`extension/popup.html`:

```html
<!doctype html>
<meta charset="utf-8" />
<title>mirrormark</title>
<style>
  body { font: 13px system-ui, sans-serif; min-width: 18em; margin: 1em; }
  .error { color: #b00020; }
</style>
<p id="db"></p>
<p id="error" class="error"></p>
<script src="popup.js"></script>
```

`extension/popup.js`:

```js
chrome.storage.local.get(["db", "status"]).then(({ db, status }) => {
  const $ = (id) => document.getElementById(id);
  if (!db) {
    $("db").textContent = "Not bound. Run `mirrormark install` to bind this profile.";
    return;
  }
  $("db").textContent = `Syncs as ${db}.`;
  if (status?.error) $("error").textContent = status.error;
});
```

- [ ] **Step 6: Write `extension/embed.go`**

```go
// Package extension holds the browser extension's files, embedded in the
// binary so one file installs everything.
package extension

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

// HostName is the native messaging host name; background.js uses the same.
const HostName = "com.github.chriscz.mirrormark"

//go:embed manifest.json background.js bind.html bind.js popup.html popup.js
var Files embed.FS

// Unpack writes the extension to dir and adds the public key to the
// manifest as "key", so Chrome derives the install's extension id from it.
func Unpack(dir string, publicKeyDER []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := Files.ReadFile(name)
		if err != nil {
			return err
		}
		if name == "manifest.json" {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return err
			}
			m["key"], _ = json.Marshal(base64.StdEncoding.EncodeToString(publicKeyDER))
			if data, err = json.MarshalIndent(m, "", "  "); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0o644)
	})
}

// Version is the extension's version from manifest.json.
func Version() (string, error) {
	data, err := Files.ReadFile("manifest.json")
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	err = json.Unmarshal(data, &m)
	return m.Version, err
}
```

- [ ] **Step 7: Run the tests, then load the extension once by hand**

Run: `bin/dev run make`
Expected: PASS.

In any Chrome you already use, open `chrome://extensions`, turn on Developer mode, Load unpacked, choose the repo's `extension/` folder (Chrome ignores `embed.go`). Expected: the card shows no errors, the service worker is listed as active or inactive (not "failed"), and the popup says "Not bound". Remove it again. Say in the commit body that this was done.

- [ ] **Step 8: Commit**

```bash
git add extension
git commit -m "feat(rfc-0001): embed the Chrome extension in the binary [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `install`, `uninstall` and `unpack` for Google Chrome

**Files:**
- Create: `internal/chrome/profiles.go`, `internal/chrome/key.go`, `internal/chrome/crx.go`, `internal/chrome/files.go`, `internal/chrome/bind.go`, `cmd/mirrormark/install.go`
- Test: `internal/chrome/chrome_test.go`, `cmd/mirrormark/install_test.go`
- Modify: `cmd/mirrormark/main.go`, `README.md`

**Interfaces:**
- Consumes: `extension.Unpack`, `extension.Version`, `extension.HostName`; `db.DefaultPaths`, `db.LoadAll`, `db.SaveDB`, `db.Connected`, `db.WriteAtomic`, `db.ValidID`; the host touches `profiles/<id>.lock` on every `hello` (task 5).
- Produces: everything under `internal/chrome` in "Interfaces between tasks". CLI output lines that task 8 parses: `Extension ID: <id>`, `Restart Chrome`, `bound: <profile dir> -> <db id>`.

**Flow of `mirrormark install [--user-data-dir DIR] [--profile DIR]...`:**

1. Read profiles from `<DIR>/Local State`; take those named by `--profile`, or all of them. Binding only creates a database file, and nothing syncs until `config.yaml` names it, so all is a safe default.
2. `chrome.Prepare`: key (`extension.pem`, created once, `0600`), unpack to `extension/` with the key, pack `mirrormark.crx`, id and version.
3. Print `Extension ID: <id>`. Note whether the two system files are already current: that means an earlier run wrote them and asked for the restart, so the extension is there.
4. If the system files are not current, write them; on a permission error write them to a temporary directory and copy them into place with `sudo install -D -m 0644`, so no mirrormark code runs as root. If that fails, stop with the error.
5. For each chosen profile, create its database file unless one already points at `<DIR>/<profile>`; create `config.yaml` with a commented example if missing.
6. If the files were not current in step 3, print the restart message and stop. Otherwise bind each profile with `chrome.Bind`, confirming through `db.Connected`, and print `bound: …` or `not bound: …`.

`mirrormark unpack` runs step 2 alone and prints the folder, for Load unpacked while developing; the key keeps the id the same as the installed one.

- [ ] **Step 1: Write the failing tests** `internal/chrome/chrome_test.go`

```go
package chrome

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestProfilesFromLocalState(t *testing.T) {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Profile 1":{"name":"Work"},"Default":{"name":"Personal"}}}}`), 0o600)
	got, err := Profiles(udd)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestProfilesWithoutLocalStateFails(t *testing.T) {
	if _, err := Profiles(t.TempDir()); err == nil {
		t.Fatal("no error for a data directory Chrome never used")
	}
}

// The public key and id of the throwaway key used in the install spike;
// Chrome itself reported this id for it.
const spikeKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA1qIVj5Wd5Sc4m9P0srXXXzh/A14mHIWQ+sWk/QuMCVCO0OM9Jym1a+KhhYEdUugY6YjaT505BgTt32PhPtnZOCAIDW3aihojoFCdidE7iHOKYMRu911k7bBOpaq/oXDdKoZO8xtg42OXaewkyHjS5KByoaIZSrWxjDT1NgCmwVi4f/Kk5lu4t4EYtD0mc+rK6288HE0tM1jIjIvW01/bAuDa5vcEwj2hVf/33yyHrm2XKVqbChnOkp4FM381bFQzYtOeNq8ec8TZTBPJEMc36OvEjurGiPCxxTOskc8bJ/4R+phVR2ADOLYu3VoiTyoFX/aYS2KJKQdJu2CX2/v98wIDAQAB"

func TestExtensionIDMatchesChrome(t *testing.T) {
	der, _ := base64.StdEncoding.DecodeString(spikeKey)
	if got := ExtensionID(der); got != "pjjjaeommfpeolloibnafkcmkindkagl" {
		t.Fatalf("id %s", got)
	}
}

func TestKeyIsCreatedOnceWithMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.pem")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil || !k1.Equal(k2) {
		t.Fatalf("second load gave another key: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

// field reads one protobuf length-delimited field.
func field(b []byte) (num int, val, rest []byte) {
	tag, n := binary.Uvarint(b)
	l, m := binary.Uvarint(b[n:])
	start := n + m
	return int(tag >> 3), b[start : start+int(l)], b[start+int(l):]
}

func TestCRXIsSignedAndHoldsTheExtension(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"x"}`), 0o644)
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k.pem"))
	out := filepath.Join(t.TempDir(), "x.crx")
	if err := WriteCRX(dir, key, out); err != nil {
		t.Fatal(err)
	}
	crx, _ := os.ReadFile(out)
	if string(crx[:4]) != "Cr24" || binary.LittleEndian.Uint32(crx[4:]) != 3 {
		t.Fatal("bad magic or version")
	}
	hlen := binary.LittleEndian.Uint32(crx[8:])
	header, archive := crx[12:12+hlen], crx[12+hlen:]

	num, proof, rest := field(header)
	if num != 2 {
		t.Fatalf("first header field %d, want 2 (sha256_with_rsa)", num)
	}
	_, pub, p2 := field(proof)
	_, sig, _ := field(p2)
	_, signed, _ := field(rest)
	if !bytes.Equal(pub, PublicKeyDER(key)) {
		t.Fatal("header carries another public key")
	}
	msg := append([]byte("CRX3 SignedData\x00"), binary.LittleEndian.AppendUint32(nil, uint32(len(signed)))...)
	msg = append(append(msg, signed...), archive...)
	h := sha256.Sum256(msg)
	pk, _ := x509.ParsePKIXPublicKey(pub)
	rsaPub, ok := pk.(*rsa.PublicKey)
	if !ok {
		t.Fatal("not an RSA key")
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, h[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "manifest.json" {
		t.Fatalf("archive: %v", err)
	}
}

func TestFilesCurrentAndWrite(t *testing.T) {
	dir := t.TempDir()
	fs := []File{{Path: filepath.Join(dir, "a", "x.json"), Data: []byte("1")}}
	if FilesCurrent(fs) {
		t.Fatal("missing file counted as current")
	}
	if err := WriteFiles(fs); err != nil {
		t.Fatal(err)
	}
	if !FilesCurrent(fs) {
		t.Fatal("written file not current")
	}
	if info, _ := os.Stat(filepath.Join(dir, "a")); info.Mode().Perm() != 0o755 {
		t.Fatalf("Chrome must be able to read the directory: %v", info.Mode().Perm())
	}
}

func TestSystemFiles(t *testing.T) {
	e := Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "0.1.0", CRX: "/h/.local/share/mirrormark/mirrormark.crx"}
	fs := SystemFiles(e, "/usr/local/bin/mirrormark")
	if fs[0].Path != "/usr/share/google-chrome/extensions/pjjjaeommfpeolloibnafkcmkindkagl.json" ||
		!bytes.Contains(fs[0].Data, []byte(`"external_version": "0.1.0"`)) {
		t.Fatalf("external file %s %s", fs[0].Path, fs[0].Data)
	}
	if fs[1].Path != "/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json" ||
		!bytes.Contains(fs[1].Data, []byte(`"chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/"`)) {
		t.Fatalf("host manifest %s %s", fs[1].Path, fs[1].Data)
	}
}

func TestBindRetriesUntilConfirmed(t *testing.T) {
	old := BindWait
	BindWait = 200 * time.Millisecond
	t.Cleanup(func() { BindWait = old })
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "google-chrome")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o755)
	calls := 0
	err := Bind(fake, filepath.Join(dir, "udd"), "Profile 1", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-work", func(time.Time) bool {
		data, _ := os.ReadFile(log)
		calls = bytes.Count(data, []byte("\n"))
		return calls >= 2
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	want := "--user-data-dir=" + filepath.Join(dir, "udd") + " --profile-directory=Profile 1 chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#chrome-work\n"
	if !bytes.HasPrefix(data, []byte(want+want)) {
		t.Fatalf("calls:\n%s", data)
	}
}
```

`cmd/mirrormark/install_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Person 1": "person-1", "  Work / Home ": "work-home", "Ünï": "n", "": ""} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChoose(t *testing.T) {
	all := []chrome.Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}}
	if got, err := choose(all, nil); err != nil || len(got) != 2 {
		t.Fatalf("default should be every profile: %+v, %v", got, err)
	}
	if got, _ := choose(all, []string{"Profile 1"}); len(got) != 1 || got[0].Dir != "Profile 1" {
		t.Fatalf("by flag: %+v", got)
	}
	if _, err := choose(all, []string{"Nope"}); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestEnsureDBMakesUniqueIDs(t *testing.T) {
	p := db.Paths{Data: t.TempDir()}
	a, err := ensureDB(p, "/u/a", chrome.Profile{Dir: "Default", Name: "Person 1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ensureDB(p, "/u/b", chrome.Profile{Dir: "Default", Name: "Person 1"})
	again, _ := ensureDB(p, "/u/a", chrome.Profile{Dir: "Default", Name: "Person 1"})
	if a != "chrome-person-1" || b != "chrome-person-1-2" || again != a {
		t.Fatalf("ids %s %s %s", a, b, again)
	}
	d, _ := db.LoadDB(p.ProfileFile(b))
	if d.Profile.Label != "chrome/person-1-2" || d.Profile.Path != "/u/b/Default" {
		t.Fatalf("profile %+v", d.Profile)
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 2 {
		t.Fatalf("files %v", dbs)
	}
}

func TestUnpackWritesTheKeyedExtension(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	var out, errb bytes.Buffer
	if code := run([]string{"unpack"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	p, _ := db.DefaultPaths()
	manifest, err := os.ReadFile(filepath.Join(p.Data, "extension", "manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte(`"key"`)) {
		t.Fatalf("manifest %s, %v", manifest, err)
	}
	if !strings.Contains(out.String(), "Extension ID: ") {
		t.Fatalf("output %q", out.String())
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `bin/dev run go test ./internal/chrome/ ./cmd/mirrormark/`
Expected: FAIL, undefined names.

- [ ] **Step 3: Write `internal/chrome/profiles.go` and `internal/chrome/key.go`**

```go
// Package chrome installs mirrormark into Google Chrome on Linux: profiles,
// the extension key and id, the CRX, the system files and profile binding.
package chrome

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Profile struct{ Dir, Name string }

// DefaultUserDataDir is Chrome's default data directory on Linux.
func DefaultUserDataDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, "google-chrome"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "google-chrome"), nil
}

// Profiles lists the profiles in Chrome's "Local State" file. The file is
// not a documented interface; it has held profile.info_cache for years.
func Profiles(userDataDir string) ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("cannot read Chrome's profile list (has Chrome run with %s?): %w", userDataDir, err)
	}
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("Local State: %w", err)
	}
	var out []Profile
	for dir, info := range ls.Profile.InfoCache {
		out = append(out, Profile{Dir: dir, Name: info.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Chrome lists no profiles in %s; start Chrome once and close it", userDataDir)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Dir, b.Dir) })
	return out, nil
}
```

```go
package chrome

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/chriscz/mirrormark/internal/db"
)

// LoadKey reads the install's private key.
func LoadKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an RSA key", path)
	}
	return rk, nil
}

// LoadOrCreateKey returns the install's key, creating it on first use.
// Keeping it means an upgrade keeps the extension id; not shipping one
// means an extension built elsewhere cannot take this install's id.
func LoadOrCreateKey(path string) (*rsa.PrivateKey, error) {
	k, err := LoadKey(path)
	if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	k, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return k, db.WriteAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

func PublicKeyDER(k *rsa.PrivateKey) []byte {
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey) // cannot fail for an RSA key
	return der
}

// ExtensionID is Chrome's id for a public key: the first 16 bytes of its
// SHA-256, each nibble written as a letter from a to p.
func ExtensionID(publicKeyDER []byte) string {
	sum := sha256.Sum256(publicKeyDER)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i], id[2*i+1] = 'a'+b>>4, 'a'+b&0xf
	}
	return string(id)
}
```

- [ ] **Step 4: Write `internal/chrome/crx.go`** (from the spike's tested writer)

```go
package chrome

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chriscz/mirrormark/internal/db"
)

// pbField encodes one protobuf length-delimited field.
func pbField(num int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

// WriteCRX packs dir as a CRX3 file signed with key: "Cr24", version 3, a
// CrxFileHeader holding the public key, the signature and the signed crx_id,
// then the zip archive.
func WriteCRX(dir string, key *rsa.PrivateKey, out string) error {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	pub := PublicKeyDER(key)
	sum := sha256.Sum256(pub)
	signedData := pbField(1, sum[:16]) // SignedData.crx_id
	msg := []byte("CRX3 SignedData\x00")
	msg = binary.LittleEndian.AppendUint32(msg, uint32(len(signedData)))
	msg = append(msg, signedData...)
	msg = append(msg, zbuf.Bytes()...)
	h := sha256.Sum256(msg)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return err
	}
	header := pbField(2, append(pbField(1, pub), pbField(2, sig)...)) // sha256_with_rsa
	header = append(header, pbField(10000, signedData)...)            // signed_header_data
	crx := []byte("Cr24")
	crx = binary.LittleEndian.AppendUint32(crx, 3)
	crx = binary.LittleEndian.AppendUint32(crx, uint32(len(header)))
	crx = append(crx, header...)
	crx = append(crx, zbuf.Bytes()...)
	return db.WriteAtomic(out, crx, 0o644)
}
```

- [ ] **Step 5: Write `internal/chrome/files.go`**

```go
package chrome

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/db"
)

const (
	// ExternalDir: on Linux, branded Chrome installs a CRX named by a JSON
	// file here into every profile, without a prompt, at its next start.
	ExternalDir = "/usr/share/google-chrome/extensions"
	// SystemHostDir: native host manifests here reach every Chrome data
	// directory, not only the default one.
	SystemHostDir = "/etc/opt/chrome/native-messaging-hosts"
)

type Extension struct{ ID, Version, Dir, CRX string }

type File struct {
	Path string
	Data []byte
}

// Prepare unpacks the embedded extension with the install's key and packs
// it as a CRX, both under dataDir.
func Prepare(dataDir string) (Extension, error) {
	key, err := LoadOrCreateKey(filepath.Join(dataDir, "extension.pem"))
	if err != nil {
		return Extension{}, err
	}
	pub := PublicKeyDER(key)
	e := Extension{ID: ExtensionID(pub), Dir: filepath.Join(dataDir, "extension"), CRX: filepath.Join(dataDir, "mirrormark.crx")}
	if e.Version, err = extension.Version(); err != nil {
		return e, err
	}
	if err := os.RemoveAll(e.Dir); err != nil {
		return e, err
	}
	if err := extension.Unpack(e.Dir, pub); err != nil {
		return e, err
	}
	return e, WriteCRX(e.Dir, key, e.CRX)
}

func jsonFile(v externalJSON) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return append(b, '\n')
}

type externalJSON struct {
	CRX     string `json:"external_crx"`
	Version string `json:"external_version"` // must equal the CRX's manifest version
}

type hostManifestJSON struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func hostManifest(bin, id string) []byte {
	b, _ := json.MarshalIndent(hostManifestJSON{Name: extension.HostName, Description: "mirrormark bookmark sync",
		Path: bin, Type: "stdio", AllowedOrigins: []string{"chrome-extension://" + id + "/"}}, "", "  ")
	return append(b, '\n')
}

func SystemFilePaths(id string) []string {
	return []string{filepath.Join(ExternalDir, id+".json"), filepath.Join(SystemHostDir, extension.HostName+".json")}
}

func SystemFiles(e Extension, bin string) []File {
	p := SystemFilePaths(e.ID)
	return []File{{p[0], jsonFile(externalJSON{CRX: e.CRX, Version: e.Version})}, {p[1], hostManifest(bin, e.ID)}}
}

func FilesCurrent(files []File) bool {
	for _, f := range files {
		if data, err := os.ReadFile(f.Path); err != nil || !bytes.Equal(data, f.Data) {
			return false
		}
	}
	return true
}

// WriteFiles writes world-readable files: Chrome reads them as the user.
func WriteFiles(files []File) error {
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := db.WriteAtomic(f.Path, f.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func RemoveFiles(paths []string) error {
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: Write `internal/chrome/bind.go`**

```go
package chrome

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// BindWait is how long Bind waits for the profile to confirm each attempt.
var BindWait = 4 * time.Second

// Bind opens bind.html#<dbID> in one profile and waits for bound to report
// the host's hello. It retries because on a profile's first start after
// the extension was installed, the page can load before the extension.
func Bind(chromeBin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error {
	def, _ := DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	for range 3 {
		start := time.Now()
		cmd := exec.Command(chromeBin, args...)
		// If Chrome is not running, this starts it; it must outlive us.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		for time.Since(start) < BindWait {
			if bound(start) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return fmt.Errorf("profile %s did not confirm; check that mirrormark is enabled in chrome://extensions there, then run mirrormark install again", profileDir)
}
```

- [ ] **Step 7: Write `cmd/mirrormark/install.go`**

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

const configExample = `# mirrormark configuration. Each union lists, for each profile id, the
# folder that profile shares. Profile ids are the file names (without .yaml)
# in ~/.local/share/mirrormark/profiles/. Paths start with toolbar, other or
# mobile. Example:
#
# unions:
#   work:
#     chrome-personal: toolbar/Work
#     chrome-work: toolbar/Work
format: 1
unions: {}
`

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("install", flag.ContinueOnError)
	fl.SetOutput(stderr)
	udd := fl.String("user-data-dir", "", "Chrome's data directory (default ~/.config/google-chrome)")
	var picks multiFlag
	fl.Var(&picks, "profile", `profile directory to bind, such as "Profile 1"; repeatable (default: all)`)
	if fl.Parse(args) != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	if *udd == "" {
		if *udd, err = chrome.DefaultUserDataDir(); err != nil {
			return fail(err)
		}
	}
	profiles, err := chrome.Profiles(*udd)
	if err != nil {
		return fail(err)
	}
	chosen, err := choose(profiles, picks)
	if err != nil {
		return fail(err)
	}
	ext, err := chrome.Prepare(p.Data)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Extension ID: %s\n", ext.ID)
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return fail(err)
	}
	system := chrome.SystemFiles(ext, self)
	// Current files mean an earlier run wrote them and asked for the
	// restart, so the extension is installed and profiles can be bound now.
	ready := chrome.FilesCurrent(system)
	if !ready {
		if err := writeSystemFiles(system, stdout, stderr); err != nil {
			return fail(fmt.Errorf("could not install into Chrome as root: %w", err))
		}
	}
	ids := map[string]string{}
	for _, c := range chosen {
		id, err := ensureDB(p, *udd, c)
		if err != nil {
			return fail(err)
		}
		ids[c.Dir] = id
	}
	if _, err := os.Stat(p.ConfigFile()); errors.Is(err, fs.ErrNotExist) {
		if err := db.WriteAtomic(p.ConfigFile(), []byte(configExample), 0o600); err != nil {
			return fail(err)
		}
	}
	if !ready {
		fmt.Fprintln(stdout, "Restart Chrome (close every window), then run mirrormark install again to bind the profiles.")
		return 0
	}
	bin, err := exec.LookPath("google-chrome")
	if err != nil {
		return fail(err)
	}
	code := 0
	for _, c := range chosen {
		id := ids[c.Dir]
		bound := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := chrome.Bind(bin, *udd, c.Dir, ext.ID, id, bound); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v\n", c.Dir, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", c.Dir, id)
	}
	if code == 0 {
		fmt.Fprintf(stdout, "Now list the folders to share in %s.\nThe host logs to %s.\n", p.ConfigFile(), p.LogFile())
	}
	return code
}

// writeSystemFiles writes Chrome's two root-owned files. If this process
// may not, it stages them in a temporary directory and copies them into
// place with `sudo install`, so no mirrormark code runs as root.
func writeSystemFiles(files []chrome.File, stdout, stderr io.Writer) error {
	err := chrome.WriteFiles(files)
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	tmp, err := os.MkdirTemp("", "mirrormark-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintf(stdout, "Writing %s and %s needs root; running sudo.\n", files[0].Path, files[1].Path)
	for i, f := range files {
		src := filepath.Join(tmp, strconv.Itoa(i))
		if err := os.WriteFile(src, f.Data, 0o644); err != nil {
			return err
		}
		if err := sudo(stdout, stderr, "install", "-D", "-m", "0644", src, f.Path); err != nil {
			return err
		}
	}
	return nil
}

func sudo(stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	return cmd.Run()
}

// choose returns the profiles named in picks, or every profile if none are.
func choose(all []chrome.Profile, picks []string) ([]chrome.Profile, error) {
	if len(picks) == 0 {
		return all, nil
	}
	var got []chrome.Profile
	for _, want := range picks {
		i := slices.IndexFunc(all, func(p chrome.Profile) bool { return p.Dir == want })
		if i < 0 {
			return nil, fmt.Errorf("no Chrome profile directory %q in %v", want, all)
		}
		got = append(got, all[i])
	}
	return got, nil
}

// ensureDB returns the database id of a profile, creating its file first
// if none points at the profile yet.
func ensureDB(p db.Paths, udd string, c chrome.Profile) (string, error) {
	path := filepath.Join(udd, c.Dir)
	dbs, _ := db.LoadAll(p)
	for id, d := range dbs {
		if d.Profile.Path == path {
			return id, nil
		}
	}
	base := slug(c.Name)
	if base == "" {
		base = slug(c.Dir)
	}
	id, label := "chrome-"+base, "chrome/"+base
	for n := 2; ; n++ {
		if _, err := os.Stat(p.ProfileFile(id)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		id, label = fmt.Sprintf("chrome-%s-%d", base, n), fmt.Sprintf("chrome/%s-%d", base, n)
	}
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: label, Browser: "chrome", Path: path}}
	return id, db.SaveDB(p.ProfileFile(id), d)
}

// slug lower-cases s and joins its runs of a-z and 0-9 with dashes.
func slug(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			gap = false
			b.WriteRune(r)
		} else {
			gap = true
		}
	}
	return b.String()
}

// cmdUnpack writes the extension with the install's key, so a Load
// unpacked copy has the same id as the installed one.
func cmdUnpack(stdout, stderr io.Writer) int {
	p, err := db.DefaultPaths()
	if err == nil {
		var ext chrome.Extension
		if ext, err = chrome.Prepare(p.Data); err == nil {
			fmt.Fprintf(stdout, "Extension ID: %s\nUnpacked to %s\n", ext.ID, ext.Dir)
			return 0
		}
	}
	fmt.Fprintln(stderr, "mirrormark:", err)
	return 1
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fl.SetOutput(stderr)
	purge := fl.Bool("purge", false, "also delete the config, databases, backups, key and log")
	if fl.Parse(args) != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	if key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem")); err == nil {
		paths := chrome.SystemFilePaths(chrome.ExtensionID(chrome.PublicKeyDER(key)))
		err := chrome.RemoveFiles(paths)
		if errors.Is(err, fs.ErrPermission) {
			err = sudo(stdout, stderr, append([]string{"rm", "-f", "--"}, paths...)...)
		}
		if err != nil {
			return fail(err)
		}
	}
	if err := chrome.RemoveFiles([]string{filepath.Join(p.Data, "mirrormark.crx")}); err != nil {
		return fail(err)
	}
	if err := os.RemoveAll(filepath.Join(p.Data, "extension")); err != nil {
		return fail(err)
	}
	if *purge {
		for _, d := range []string{p.Data, p.State} {
			if err := os.RemoveAll(d); err != nil {
				return fail(err)
			}
		}
		if err := chrome.RemoveFiles([]string{p.ConfigFile()}); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintln(stdout, "Chrome removes mirrormark from every profile when it next starts.")
	return 0
}
```

- [ ] **Step 8: Wire the subcommands into `main.go`**

In `run`, add:

```go
	case "install":
		return cmdInstall(args[1:], stdout, stderr)
	case "uninstall":
		return cmdUninstall(args[1:], stdout, stderr)
	case "unpack":
		return cmdUnpack(stdout, stderr)
```

Usage lines:

```
  install     Install into Google Chrome and bind profiles [--user-data-dir DIR] [--profile DIR]...
  uninstall   Remove from Google Chrome [--purge]
  unpack      Write the extension, with this install's key, for Load unpacked
```

- [ ] **Step 9: Run the tests**

Run: `bin/dev run make`
Expected: PASS.

- [ ] **Step 10: Docs**

README: replace "Status: in development…" with a short status line and add, before "Files":

```markdown
## Install (Google Chrome on Linux)

You need the `mirrormark` binary (`make` builds `build/mirrormark`; copy it
somewhere stable such as `~/.local/bin`, because Chrome runs it from there).

1. Close Chrome, or keep it open: either works.
2. Run `mirrormark install`. It binds every Chrome profile, or only those
   you name with `--profile "Profile 1"` (repeat it for more). A bound
   profile syncs nothing until `config.yaml` names it. It asks for your
   password once, through `sudo`, to copy two files that tell Chrome about
   the extension. This installs it in every profile of every Chrome on this
   computer; profiles you don't bind ignore it.
3. Restart Chrome (close every window), then run `mirrormark install` again.
   It opens a tab in each profile to bind it, and closes it.
4. Edit `~/.config/mirrormark/config.yaml` to share folders (see below).
   Changes appear in the other profiles within a few seconds.

Each bound profile gets an id such as `chrome-work` and a label such as
`chrome/work` (used in conflict copies). Edit the label in
`~/.local/share/mirrormark/profiles/<id>.yaml` if you like.

`mirrormark unpack` writes the extension, with the same id as the installed
one, to `~/.local/share/mirrormark/extension/` for Load unpacked while
developing. It still needs `install`'s files to reach the host.

Upgrading: replace the binary and run `mirrormark install` again, then
restart Chrome when it asks.

Uninstalling: `mirrormark uninstall` (add `--purge` to delete your
mirrormark files too). Your bookmarks stay in Chrome.

Before you add a union, export your bookmarks from Chrome
(Bookmark manager → Export) so you have a copy.
```

- [ ] **Step 11: Commit**

```bash
git add internal/chrome cmd/mirrormark README.md
git commit -m "feat(rfc-0001): install, bind and uninstall for Google Chrome on Linux [new feature]

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: E2E harness and the install-and-bind test

**Driver choice.** The tests are Go (`//go:build e2e`) and drive Chrome over the DevTools protocol on `--remote-debugging-pipe`: Chrome reads NUL-terminated JSON commands on fd 3 and writes replies on fd 4. That needs `os.Pipe`, `exec.Cmd.ExtraFiles` and `encoding/json`, about 80 lines, and no WebSocket client. Alternatives: chromedp (a dozen transitive modules and its own launcher, which we would have to fight to keep extensions enabled), a stdlib WebSocket client (`--remote-debugging-port` plus ~150 lines of framing), or Puppeteer (Node and npm in the image for tests alone). The pipe keeps one language, no dependency, and the spike confirmed it works in branded Chrome 155 under Xvfb, including forwarding a second `google-chrome` invocation to the running instance, which binding relies on.

Each test gets its own `$HOME` and two Chrome data directories, `chrome-a` and `chrome-b` (two Chrome instances, each with its `Default` profile). The tests run `build/mirrormark install` exactly as a user would; the image makes the two system directories writable so no `sudo` is needed.

**Files:**
- Create: `e2e/cdp.go`, `e2e/browser.go`, `e2e/env.go`, `e2e/install_test.go`
- Modify: `Dockerfile`, `Makefile`, `README.md`

**Interfaces:**
- Consumes: the CLI output lines of task 7 (`Extension ID: `, `Restart Chrome`, `bound: Default -> <id>`); `db.Paths`, `db.LoadDB`, `db.Connected`, `db.WriteAtomic`, `db.Lock`; `fakebrowser.NodeLines`; the extension's `popup.html` as a page to run `chrome.*` calls in.
- Produces for task 9: `setup(t) *Env` with `A`, `B`, `DBA`, `DBB`, `LabelA`, `LabelB`, `ExtID`, `Paths`; `(*Env).Union(t)`, `(*Env).launch(name)`, `(*Env).mm(t, args...)`; `(*Browser).Lines/Mkdir/Add/Rename/SetURL/Move/Remove/Close/open`; `waitLines(t, b, folder, want, within) time.Duration`; `eventually(t, within, what, ok)`.

- [ ] **Step 1: Add Chrome and Xvfb to the Dockerfile** (after the Go block)

```dockerfile
# Google Chrome stable and Xvfb for the end-to-end tests (make check-e2e).
# Google removes old packages from its pool; when this download fails, bump
# the version and checksum together.
ARG CHROME_VERSION=155.0.8059.79-1
ARG CHROME_SHA256=b7726070ca707965e9a17b38e4f15ba283bf6f65c339626c092862af7b19badc
RUN curl -fsSLo /tmp/chrome.deb "https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_${CHROME_VERSION}_amd64.deb" \
 && echo "${CHROME_SHA256}  /tmp/chrome.deb" | sha256sum -c - \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/chrome.deb xvfb xauth \
 && rm -rf /var/lib/apt/lists/* /tmp/chrome.deb

# `mirrormark install` writes into these root-owned directories. The tests
# run as your UID without sudo, so in this image only, anyone may write
# them; the sticky bit stops users removing each other's files.
RUN mkdir -p /usr/share/google-chrome/extensions /etc/opt/chrome/native-messaging-hosts \
 && chmod 1777 /usr/share/google-chrome/extensions /etc/opt/chrome/native-messaging-hosts
```

- [ ] **Step 2: Add the Makefile targets**

```make
check-vet:
	$(GO) vet ./...
	$(GO) vet -tags e2e ./e2e/
.PHONY: check-vet

# Not part of `check`: needs Chrome and Xvfb from the dev image and takes
# minutes. Each test starts two Chrome instances.
check-e2e: build
	MIRRORMARK_BIN=$(abspath $(BIN)) xvfb-run -a -s "-screen 0 1280x800x24" \
		$(GO) test -tags e2e -count=1 -timeout 30m -v ./e2e/ $(if $(RUN),-run '$(RUN)')
.PHONY: check-e2e
```

`make check-e2e RUN=TestEditsPropagate` runs one test.

- [ ] **Step 3: Write `e2e/cdp.go`**

```go
//go:build e2e

// Package e2e drives two real Google Chrome instances through the DevTools
// protocol to test mirrormark from install to sync.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// cdp speaks the DevTools protocol over --remote-debugging-pipe: JSON
// messages ended by a NUL byte, commands on Chrome's fd 3, replies on fd 4.
type cdp struct {
	w    *os.File
	mu   sync.Mutex
	next int
	wait map[int]chan reply
	dead chan struct{}
}

type reply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type request[P any] struct {
	ID        int    `json:"id"`
	Method    string `json:"method"`
	SessionID string `json:"sessionId,omitempty"`
	Params    P      `json:"params"`
}

type none struct{}

func newCDP(w, r *os.File) *cdp {
	c := &cdp{w: w, wait: map[int]chan reply{}, dead: make(chan struct{})}
	go c.read(r)
	return c
}

// read delivers replies by id. Events carry no id; the tests need none.
func (c *cdp) read(r io.Reader) {
	defer close(c.dead)
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		var m reply
		if json.Unmarshal(b[:len(b)-1], &m) != nil || m.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.wait[m.ID]
		delete(c.wait, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func call[P, R any](c *cdp, session, method string, params P) (R, error) {
	var out R
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan reply, 1)
	c.wait[id] = ch
	b, err := json.Marshal(request[P]{ID: id, Method: method, SessionID: session, Params: params})
	if err == nil {
		_, err = c.w.Write(append(b, 0))
	}
	c.mu.Unlock()
	if err != nil {
		return out, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return out, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return out, json.Unmarshal(m.Result, &out)
	case <-c.dead:
		return out, fmt.Errorf("%s: Chrome closed the pipe", method)
	case <-time.After(30 * time.Second):
		return out, fmt.Errorf("%s: no reply within 30 s", method)
	}
}
```

- [ ] **Step 4: Write `e2e/browser.go`**

```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"testing"
	"time"
)

type Browser struct {
	Name    string
	UDD     string
	cmd     *exec.Cmd
	c       *cdp
	session string // an extension page; chrome.* calls run there
	done    chan struct{}
}

func launch(t *testing.T, name, udd string, env []string) *Browser {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	logf, err := os.OpenFile(udd+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("google-chrome", "--user-data-dir="+udd, "--no-sandbox", "--no-first-run",
		"--no-default-browser-check", "--disable-dev-shm-usage", "--password-store=basic",
		"--remote-debugging-pipe", "about:blank")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logf, logf
	cmd.ExtraFiles = []*os.File{inR, outW} // fds 3 and 4 in Chrome
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	b := &Browser{Name: name, UDD: udd, cmd: cmd, c: newCDP(inW, outR), done: make(chan struct{})}
	go func() { cmd.Wait(); logf.Close(); close(b.done) }()
	t.Cleanup(b.Close)
	if _, err := call[none, none](b.c, "", "Browser.getVersion", none{}); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

// Close shuts Chrome down through the protocol, so it writes Local State
// and Preferences as a user's quit would.
func (b *Browser) Close() {
	select {
	case <-b.done:
		return
	default:
	}
	call[none, none](b.c, "", "Browser.close", none{})
	select {
	case <-b.done:
	case <-time.After(20 * time.Second):
		b.cmd.Process.Kill()
		<-b.done
	}
}

// open loads the extension's popup page in a tab and attaches to it. It
// retries because on a profile's first start the extension installs after
// the first tab has loaded.
func (b *Browser) open(t *testing.T, extID string) {
	t.Helper()
	type createParams struct {
		URL string `json:"url"`
	}
	type attachParams struct {
		TargetID string `json:"targetId"`
		Flatten  bool   `json:"flatten"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tg, err := call[createParams, struct {
			TargetID string `json:"targetId"`
		}](b.c, "", "Target.createTarget", createParams{URL: "chrome-extension://" + extID + "/popup.html"})
		if err != nil {
			t.Fatal(err)
		}
		at, err := call[attachParams, struct {
			SessionID string `json:"sessionId"`
		}](b.c, "", "Target.attachToTarget", attachParams{TargetID: tg.TargetID, Flatten: true})
		if err != nil {
			t.Fatal(err)
		}
		b.session = at.SessionID
		for range 20 {
			if s, err := evalJS[string](b, "typeof chrome.bookmarks"); err == nil && s == "object" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("%s: the extension page never loaded", b.Name)
}

type evalParams struct {
	Expression    string `json:"expression"`
	AwaitPromise  bool   `json:"awaitPromise"`
	ReturnByValue bool   `json:"returnByValue"`
}

type evalResult struct {
	Result struct {
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// prelude: paths are below the bookmarks bar, such as "Work/Dev/GitHub".
const prelude = `
const mm = {
  async folder(path) {
    const [root] = await chrome.bookmarks.getTree();
    let n = root.children.find((c) => c.folderType === "bookmarks-bar");
    for (const seg of path.split("/").filter(Boolean)) {
      n = (n.children ?? []).find((c) => !c.url && c.title === seg);
      if (!n) return null;
    }
    return n;
  },
  async node(path) {
    const i = path.lastIndexOf("/");
    const f = await mm.folder(path.slice(0, Math.max(i, 0)));
    const n = f?.children?.find((c) => c.title === path.slice(i + 1));
    if (!n) throw new Error("nothing at " + path);
    return n;
  },
  async lines(path) {
    const f = await mm.folder(path);
    if (!f) return null;
    const out = [];
    const walk = (n, p) => {
      for (const c of n.children ?? []) {
        const q = p + "/" + c.title;
        out.push(c.url ? q + " " + c.url : q + "/");
        walk(c, q);
      }
    };
    walk(f, "");
    return out.sort();
  },
};
`

func evalJS[R any](b *Browser, expr string) (R, error) {
	var out R
	res, err := call[evalParams, evalResult](b.c, b.session, "Runtime.evaluate", evalParams{
		Expression: "(async () => {" + prelude + "return await (" + expr + ");})()", AwaitPromise: true, ReturnByValue: true})
	if err != nil {
		return out, err
	}
	if d := res.ExceptionDetails; d != nil {
		return out, fmt.Errorf("%s: %s %s", expr, d.Text, d.Exception.Description)
	}
	if len(res.Result.Value) > 0 {
		err = json.Unmarshal(res.Result.Value, &out)
	}
	return out, err
}

func eval[R any](t *testing.T, b *Browser, expr string) R {
	t.Helper()
	r, err := evalJS[R](b, expr)
	if err != nil {
		t.Fatalf("%s: %v", b.Name, err)
	}
	return r
}

// q quotes s as a JavaScript string.
func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Lines lists folder (below the bar) as sorted "path url" lines; nil if
// the folder does not exist.
func (b *Browser) Lines(t *testing.T, folder string) []string {
	return eval[[]string](t, b, "mm.lines("+q(folder)+")")
}

func (b *Browser) Mkdir(t *testing.T, p string) {
	eval[string](t, b, fmt.Sprintf("mm.folder(%s).then((f) => chrome.bookmarks.create({parentId: f.id, title: %s})).then(() => '')",
		q(path.Dir("/"+p)), q(path.Base(p))))
}

func (b *Browser) Add(t *testing.T, folder, title, url string) {
	eval[string](t, b, fmt.Sprintf("mm.folder(%s).then((f) => chrome.bookmarks.create({parentId: f.id, title: %s, url: %s})).then(() => '')",
		q(folder), q(title), q(url)))
}

func (b *Browser) Rename(t *testing.T, p, title string) {
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => chrome.bookmarks.update(n.id, {title: %s})).then(() => '')", q(p), q(title)))
}

func (b *Browser) SetURL(t *testing.T, p, url string) {
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => chrome.bookmarks.update(n.id, {url: %s})).then(() => '')", q(p), q(url)))
}

func (b *Browser) Move(t *testing.T, p, folder string) {
	eval[string](t, b, fmt.Sprintf("Promise.all([mm.node(%s), mm.folder(%s)]).then(([n, f]) => chrome.bookmarks.move(n.id, {parentId: f.id})).then(() => '')", q(p), q(folder)))
}

func (b *Browser) Remove(t *testing.T, p string) {
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => n.url ? chrome.bookmarks.remove(n.id) : chrome.bookmarks.removeTree(n.id)).then(() => '')", q(p)))
}
```

`path.Dir("/Work")` is `/`, which `mm.folder` reads as the bar itself, so `Mkdir(t, "Work")` creates a folder on the bar.

- [ ] **Step 5: Write `e2e/env.go`**

```go
//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
)

var bin = os.Getenv("MIRRORMARK_BIN")

func TestMain(m *testing.M) {
	if bin == "" {
		fmt.Fprintln(os.Stderr, "MIRRORMARK_BIN is not set; run: bin/dev run make check-e2e")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// Env is one test's world: a home directory, two Chrome instances with
// their Default profiles bound to two database files.
type Env struct {
	t              *testing.T
	Home           string
	Paths          db.Paths
	env            []string
	ExtID          string
	A, B           *Browser
	DBA, DBB       string
	LabelA, LabelB string
}

var (
	extIDRe = regexp.MustCompile(`Extension ID: ([a-p]{32})`)
	boundRe = regexp.MustCompile(`bound: Default -> ([a-z0-9-]+)`)
)

// setup installs and binds as a user would: Chrome runs once (so its
// profile list exists), install writes the files and asks for a restart,
// Chrome restarts, and install runs again for each data directory to bind.
func setup(t *testing.T) *Env {
	home := t.TempDir()
	e := &Env{t: t, Home: home, Paths: db.Paths{
		Config: filepath.Join(home, ".config", "mirrormark"),
		Data:   filepath.Join(home, ".local", "share", "mirrormark"),
		State:  filepath.Join(home, ".local", "state", "mirrormark"),
	}}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "XDG_") {
			e.env = append(e.env, kv)
		}
	}
	e.env = append(e.env, "HOME="+home)

	e.launch("a").Close()
	e.launch("b").Close()
	out := e.mm(t, "install", "--user-data-dir", e.udd("a"))
	m := extIDRe.FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "Restart Chrome") {
		t.Fatalf("first install did not ask for a restart:\n%s", out)
	}
	e.ExtID = m[1]
	// Runs before t.TempDir's cleanup: later tests must not see this
	// test's extension in the shared system directories.
	t.Cleanup(func() { e.mm(t, "uninstall") })

	e.A, e.B = e.launch("a"), e.launch("b")
	e.DBA, e.DBB = e.bind("a"), e.bind("b")
	e.A.open(t, e.ExtID)
	e.B.open(t, e.ExtID)
	e.LabelA, e.LabelB = e.label(e.DBA), e.label(e.DBB)
	return e
}

func (e *Env) udd(name string) string      { return filepath.Join(e.Home, "chrome-"+name) }
func (e *Env) launch(name string) *Browser { return launch(e.t, name, e.udd(name), e.env) }

// mm runs the mirrormark binary as the test's user.
func (e *Env) mm(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env, cmd.Stdin = e.env, strings.NewReader("")
	out, err := cmd.CombinedOutput()
	t.Logf("mirrormark %s:\n%s", strings.Join(args, " "), out)
	if err != nil {
		t.Fatalf("mirrormark %v: %v", args, err)
	}
	return string(out)
}

func (e *Env) bind(name string) string {
	out := e.mm(e.t, "install", "--user-data-dir", e.udd(name))
	m := boundRe.FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("install did not bind %s:\n%s", name, out)
	}
	return m[1]
}

func (e *Env) label(id string) string {
	d, err := db.LoadDB(e.Paths.ProfileFile(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Profile.Label
}

// Union shares toolbar/Work of both profiles as union "work" and waits for
// the first rounds to create the folders.
func (e *Env) Union(t *testing.T) {
	t.Helper()
	cfg := fmt.Sprintf("format: 1\nunions:\n  work:\n    %s: toolbar/Work\n    %s: toolbar/Work\n", e.DBA, e.DBB)
	if err := db.WriteAtomic(e.Paths.ConfigFile(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "member folders", func() bool {
		return e.A.Lines(t, "Work") != nil && e.B.Lines(t, "Work") != nil
	})
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", within, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitLines waits until folder in b holds want and returns how long it took.
func waitLines(t *testing.T, b *Browser, folder string, want []string, within time.Duration) time.Duration {
	t.Helper()
	slices.Sort(want)
	start := time.Now()
	var got []string
	for time.Since(start) < within {
		if got = b.Lines(t, folder); slices.Equal(got, want) {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s/%s after %v:\n got %q\nwant %q", b.Name, folder, within, got, want)
	return 0
}
```

Note: if Chrome's first run does not write `Local State` with `profile.info_cache` for a fresh data directory, `install` fails with "Chrome lists no profiles". Check this first when you run the test (Step 7); if it happens, make the first launch open a page and wait 2 s before `Close`, and say why in a comment there.

- [ ] **Step 6: Write `e2e/install_test.go`**

```go
//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
)

func TestInstallAndBind(t *testing.T) {
	e := setup(t)
	if e.DBA == e.DBB {
		t.Fatalf("both profiles bound to %s", e.DBA)
	}
	for _, c := range []struct {
		b  *Browser
		id string
	}{{e.A, e.DBA}, {e.B, e.DBB}} {
		if got := eval[string](t, c.b, `chrome.storage.local.get("db").then((r) => r.db)`); got != c.id {
			t.Errorf("%s is bound to %q, want %q", c.b.Name, got, c.id)
		}
		d, err := db.LoadDB(e.Paths.ProfileFile(c.id))
		if err != nil || d.Profile.Path != filepath.Join(c.b.UDD, "Default") {
			t.Errorf("%s database %+v, %v", c.id, d, err)
		}
	}
	// Installing again keeps the binding.
	if out := e.mm(t, "install", "--user-data-dir", e.A.UDD); !strings.Contains(out, "bound: Default -> "+e.DBA) {
		t.Fatalf("second install rebound to another database:\n%s", out)
	}
	// A restart keeps the binding: the service worker starts with the
	// profile and connects again without any page open.
	before := time.Now()
	e.A.Close()
	e.A = e.launch("a")
	eventually(t, 15*time.Second, "a hello after restart", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
}
```

- [ ] **Step 7: Run it**

Run: `bin/dev image build && bin/dev run make check-e2e`
Expected: both tests PASS in a few minutes. On failure, the Chrome logs are in the test's temp home (`chrome-a.log`, `chrome-b.log`) and the host log in `.local/state/mirrormark/log`; rerun with `t.TempDir` swapped for a fixed directory if you need to inspect them after the run.

Also run: `bin/dev run make`
Expected: PASS (it vets the e2e package but does not run it).

- [ ] **Step 8: Docs**

README, in "Development":

```markdown
    bin/dev run make check-e2e                  # end-to-end tests in real Chrome
    bin/dev run make check-e2e RUN=TestEditsPropagate  # one of them

The end-to-end tests start two Google Chrome instances under Xvfb in the
dev image, install mirrormark into them with `mirrormark install` and
drive them over the DevTools protocol. They take a few minutes.
```

- [ ] **Step 9: Commit**

```bash
git add Dockerfile Makefile e2e README.md
git commit -m "test(rfc-0001): install and bind in two Chrome instances end to end

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: E2E union sync tests

**Files:**
- Create: `e2e/sync_test.go`
- Modify: `README.md` (only if a test shows the README is wrong)

**Interfaces:**
- Consumes: everything task 8 produces; `db.Lock`, `db.LoadDB`; `fakebrowser.NodeLines`.
- Produces: nothing further.

A bug these tests find in an earlier task is fixed in that task's commit, with a unit test that fails without the fix, so every commit in the stack stays correct on its own: `git commit --fixup=<sha of that task>` and then `GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash <base>` (no interactive editor needed). A bug outside this plan's work goes into `TASKS.md`.

- [ ] **Step 1: Write the tests** `e2e/sync_test.go`

```go
//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
)

const (
	gh  = "https://github.com/"
	lwn = "https://lwn.net/"
)

// syncTimeout is generous; TestEditsPropagate checks the 3-second target.
const syncTimeout = 10 * time.Second

// synced puts one bookmark in A's Work and waits for it in B.
func synced(t *testing.T) *Env {
	e := setup(t)
	e.Union(t)
	e.A.Add(t, "Work", "GitHub", gh)
	waitLines(t, e.B, "Work", []string{"/GitHub " + gh}, syncTimeout)
	return e
}

func both(t *testing.T, e *Env, want ...string) {
	t.Helper()
	waitLines(t, e.A, "Work", want, syncTimeout)
	waitLines(t, e.B, "Work", want, syncTimeout)
}

// TestEditsPropagate adds, renames, makes a folder and moves, each in one
// browser, and checks that every edit reaches the other within 3 seconds.
func TestEditsPropagate(t *testing.T) {
	e := synced(t)
	var worst time.Duration
	reach := func(b *Browser, want ...string) {
		t.Helper()
		d := waitLines(t, b, "Work", want, syncTimeout)
		t.Logf("reached %s after %v", b.Name, d)
		worst = max(worst, d)
	}
	e.B.Add(t, "Work", "News", lwn)
	reach(e.A, "/GitHub "+gh, "/News "+lwn)
	e.A.Rename(t, "Work/GitHub", "GH")
	reach(e.B, "/GH "+gh, "/News "+lwn)
	e.A.Mkdir(t, "Work/Dev")
	reach(e.B, "/Dev/", "/GH "+gh, "/News "+lwn)
	e.A.Move(t, "Work/GH", "Work/Dev")
	reach(e.B, "/Dev/", "/Dev/GH "+gh, "/News "+lwn)
	both(t, e, "/Dev/", "/Dev/GH "+gh, "/News "+lwn)
	if worst > 3*time.Second {
		t.Fatalf("worst latency %v; the target is 3 s", worst)
	}
}

func TestDeleteMovesToDeletedEverywhere(t *testing.T) {
	e := synced(t)
	e.B.Remove(t, "Work/GitHub")
	both(t, e, "/_deleted/", "/_deleted/GitHub "+gh)
	// Purging from _deleted removes it everywhere.
	e.A.Remove(t, "Work/_deleted/GitHub")
	both(t, e, "/_deleted/")
}

func TestConflictingEditsMakeSuffixedCopies(t *testing.T) {
	e := synced(t)
	// Holding the global lock stops both hosts' rounds, so both edits are
	// made before either profile sees the other's.
	unlock, err := db.Lock(e.Paths.GlobalLock())
	if err != nil {
		t.Fatal(err)
	}
	e.A.SetURL(t, "Work/GitHub", gh+"orgs/a")
	e.B.SetURL(t, "Work/GitHub", gh+"orgs/b")
	time.Sleep(1500 * time.Millisecond) // past the extension's 0.5 s quiet time
	unlock()
	both(t, e,
		fmt.Sprintf("/GitHub (%s) %sorgs/a", e.LabelA, gh),
		fmt.Sprintf("/GitHub (%s) %sorgs/b", e.LabelB, gh))
}

func TestFirstSyncUnionsExistingBookmarks(t *testing.T) {
	e := setup(t)
	e.A.Mkdir(t, "Work")
	e.A.Add(t, "Work", "GitHub", gh)
	e.A.Add(t, "Work", "Mail", "https://fastmail.com/")
	e.B.Mkdir(t, "Work")
	e.B.Add(t, "Work", "GitHub", gh)
	e.B.Add(t, "Work", "Mail", "https://gmail.com/")
	e.B.Add(t, "Work", "News", lwn)
	e.Union(t)
	both(t, e,
		"/GitHub "+gh,
		fmt.Sprintf("/Mail (%s) https://fastmail.com/", e.LabelA),
		fmt.Sprintf("/Mail (%s) https://gmail.com/", e.LabelB),
		"/News "+lwn)
}

func TestClosedBrowserCatchesUpOnStart(t *testing.T) {
	e := synced(t)
	e.B.Close()
	e.A.Add(t, "Work", "News", lwn)
	eventually(t, syncTimeout, "A to publish News", func() bool {
		d, err := db.LoadDB(e.Paths.ProfileFile(e.DBA))
		return err == nil && d.Unions["work"] != nil &&
			slices.Contains(fakebrowser.NodeLines(d.Unions["work"].Children), "/News "+lwn)
	})
	e.B = e.launch("b")
	// B's host writes B's file in the round that applies News; that write
	// must come within 3 s of B's extension connecting.
	eventually(t, 30*time.Second, "B to catch up", func() bool {
		d, err := db.LoadDB(e.Paths.ProfileFile(e.DBB))
		return err == nil && d.Unions["work"] != nil &&
			slices.Contains(fakebrowser.NodeLines(d.Unions["work"].Children), "/News "+lwn)
	})
	// The host touches the lock file on hello.
	hello, err := os.Stat(e.Paths.ProfileLock(e.DBB))
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := os.Stat(e.Paths.ProfileFile(e.DBB))
	if err != nil {
		t.Fatal(err)
	}
	if d := wrote.ModTime().Sub(hello.ModTime()); d > 3*time.Second {
		t.Errorf("B caught up %v after connecting; want at most 3 s", d)
	}
	e.B.open(t, e.ExtID)
	waitLines(t, e.B, "Work", []string{"/GitHub " + gh, "/News " + lwn}, syncTimeout)
}

```

Note on `TestClosedBrowserCatchesUpOnStart`: B's file changes only in the round that applies News, so its mtime is the catch-up write.

- [ ] **Step 2: Run them**

Run: `bin/dev run make check-e2e`
Expected: all tests in `e2e/` PASS; `TestEditsPropagate` logs four latencies under 3 s (expected around 1.5–2 s: 0.5 s quiet time, A's round, up to 1 s until B polls, B's round).

When a test fails, find the cause with `superpowers:systematic-debugging`: read the host log (`<home>/.local/state/mirrormark/log`) and the database files of the run. Fix the code, and add a unit test in the package at fault that fails without the fix.

- [ ] **Step 3: Run the whole suite once more, three times in a row**

Run: `for i in 1 2 3; do bin/dev run make check-e2e || break; done`
Expected: three clean runs. A test that fails only sometimes is a bug (usually a missing wait or a race in the host); fix it, don't retry it.

- [ ] **Step 4: Commit**

```bash
git add e2e
git commit -m "test(rfc-0001): cover union sync end to end in two Chrome instances

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## After the last task

- Run `bin/dev run make && bin/dev run make check-e2e` on the final stack.
- The e2e image makes the system directories writable, so the `sudo install` path never runs there. Run `mirrormark install` once where they are root-owned (the developer's machine below is enough) and record that it asked for `sudo` once and wrote both files `0644`.
- Run once on the developer's own Chrome with two test profiles, and record it in the PR.
- Keep the RFC true: if the implementation changed anything the RFC says, update it in its own `docs(rfc-0001): ...` commit at the end of the stack. The RFC stays `draft` until the stack is merged; then close it out with `rfc-craft` (`in review`, then `accepted`).

## Risks

- **Google drops old Chrome packages.** The pinned `.deb` URL will stop working some day; the Dockerfile says to bump version and checksum together. A Chrome upgrade can change the undocumented parts this relies on (`Local State`, the external-extensions directory, command-line forwarding of `chrome-extension://` URLs).
- **`Local State` after a first run.** The tests assume Chrome writes `profile.info_cache` when a fresh data directory closes cleanly. Task 8 Step 5 says what to do if not.
- **Merge corner cases.** The merge code is new and subtle (deletion as a move, purges, resurrection, cycles, retries). The unit tests pin the RFC's examples and the property test explores histories, but a convergence failure in the property test is the most likely place for implementation time to go. Do not weaken invariants to pass it.
- **Latency.** The 3-second target leaves about 1 s of slack over the expected 1.5–2 s. A slow CI machine can push `TestEditsPropagate` over; if so, measure where the time goes before changing any timing.
- **One slow browser stalls all.** A round holds the global lock while its browser applies ops (up to 10 s); acceptable for two or three profiles, as the RFC says.
- **The system host manifest names the installing user's binary.** Other OS users on the same machine get the extension but stay unbound, so it never starts the host for them.
- **Errors repeat in the log.** A skipped union warns on every round (each file change). Noisy, but the log is the only place besides the popup that says why a union is not syncing.
- **Tests share the system directories.** Tests run one at a time and uninstall in cleanup; do not add `t.Parallel()` to e2e tests.
