# Tasks

Bugs and follow-ups found along the way that aren't the current RFC's job.
Each entry says where, what, its effect, how it was found and the fix.

## Open

### `TestTwoHostsSyncAUnion` can fail under heavy load

- **Where:** `internal/host/host_test.go`, `TestTwoHostsSyncAUnion`.
- **What:** the test sleeps a fixed 200 ms before checking that profile a's
  database file has stopped changing.
- **Effect:** under heavy load it fails with "a's database file is still
  being written". Seen once; it didn't recur in 40 runs of the test alone.
- **Found:** a full `make` while the machine was heavily loaded,
  2026-10-15.
- **Fix:** wait for a's file to stay unchanged for a bounded time instead of
  a fixed sleep.

### Wrong-binding detection relies on browser ids not matching

- **Where:** `internal/merge/merge.go`, `wrongTree()`.
- **What:** a profile bound to the wrong database is detected when most of
  its node ids are missing from the base. Chrome's bookmark ids are small
  integers in every profile, so a wrong profile can share many ids by chance.
- **Effect:** a wrongly bound profile could slip past that clause; the empty
  folder and "too much would go" clauses still apply. `install` binds each
  profile itself, which makes a wrong binding unlikely.
- **Found:** the final review of the Chrome work, 2026-10-15.
- **Fix:** compare the profile identity the extension reports (or a token
  stored in the profile at bind time) instead of relying on ids.

### The host log grows within one session

- **Where:** `cmd/mirrormark/main.go` (rotation), `internal/host`.
- **What:** the log rotates at 1 MiB only when a host starts. A failure that
  lasts adds a few lines every 5 s for as long as the host runs.
- **Effect:** a long-running host with a lasting failure keeps writing to
  one log file.
- **Found:** the final fix wave, 2026-10-15.
- **Fix:** rotate on write as well as on start.

## Manual checks still to do

- Run `mirrormark install` from the README on a real Linux desktop with
  Google Chrome and real `sudo` (the Docker image makes the system
  directories writable, so the e2e tests never run `sudo`). Check that the
  data piped to `sudo install -D -m 0644 /dev/stdin <target>` gets through
  while `sudo` asks for a password, and that `uninstall`'s `sudo rm` works.
