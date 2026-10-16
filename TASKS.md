# Tasks

Bugs and follow-ups found along the way that aren't the current RFC's job.
Each entry says where, what, its effect, how it was found and the fix.

## Open

### Wrong-binding detection relies on browser ids not matching

- **Where:** `internal/merge/merge.go`, `wrongTree()`.
- **What:** a profile bound to the wrong database is detected when most of
  its node ids are missing from the base. Chrome's bookmark ids are small
  integers in every profile, so a wrong profile can share many ids by chance.
  The token now ties a connection to its database, but it does not tell a
  wrongly bound profile, so this entry stays.
- **Effect:** a wrongly bound profile could slip past that clause; the empty
  folder and "too much would go" clauses still apply. `install` binds each
  profile itself, which makes a wrong binding unlikely.
- **Found:** the final review of the Chrome work, 2026-10-15.
- **Fix:** compare the profile identity the extension reports (or a token
  stored in the profile at bind time) instead of relying on ids.

### The host log grows within one session

- **Where:** `cmd/mirrormark/main.go` (rotation), `internal/host`.
- **What:** the log rotates at 1 MiB only when a host starts. A failure that
  lasts adds a few lines every 5 s for as long as the host runs. A session is
  now the server's whole run, usually the user's login, which makes rotating
  on write more worth doing.
- **Effect:** a long-running host with a lasting failure keeps writing to
  one log file.
- **Found:** the final fix wave, 2026-10-15.
- **Fix:** rotate on write as well as on start.

### `DefaultRoot` never tries `~/.config/mozilla/firefox` under another `$XDG_CONFIG_HOME`

- **Where:** `internal/firefox/profiles.go`, `DefaultRoot()`.
- **What:** when `$XDG_CONFIG_HOME` is set, it looks only in
  `$XDG_CONFIG_HOME/mozilla/firefox`, then falls back to the legacy
  `~/.mozilla/firefox`. Whether Firefox itself honours `$XDG_CONFIG_HOME`
  for its profiles root is not verified.
- **Effect:** if Firefox ignores `$XDG_CONFIG_HOME` and keeps its profiles
  in `~/.config/mozilla/firefox`, `install` lands on the legacy root and
  finds no profiles or the wrong ones. `--user-data-dir` works around it.
- **Found:** the final review of the Firefox work, 2026-10-15.
- **Fix:** settle it in the desktop check below; if Firefox ignores the
  variable, try `~/.config/mozilla/firefox` too.

### A nameless `[ProfileN]` in `profiles.ini` gets an empty name

- **Where:** `internal/firefox/profiles.go`, `Profiles()`.
- **What:** a profile section with a `Path=` but no `Name=` is listed with
  an empty name, instead of falling back to the name the scan would give it
  (what follows the directory's first `.`).
- **Effect:** `install` prints an empty profile name, and the database id
  slug comes from the directory. Rare: Firefox always writes `Name=`.
- **Found:** review of the profile listing, 2026-10-15.
- **Fix:** when `Name` is empty, name the profile as the scan does.

### `--purge` cannot finish while a database names an unknown browser

- **Where:** `cmd/mirrormark/install.go`, `finishUninstall()`.
- **What:** a database whose `browser:` is neither `chrome` nor `firefox`
  (only possible by editing the file) counts as another browser's, so
  `--purge` keeps the config, log and data directory and says that
  uninstalling "their browser" with `--purge` deletes them. No uninstall
  exists for that browser.
- **Effect:** the message is a dead end; the user has to delete the files
  by hand. The shared files are kept, which is safe.
- **Found:** review of per-browser purge, 2026-10-15.
- **Fix:** name the databases whose browser is unknown and tell the user to
  move them out of the profiles directory, or delete them, first.

### One unreadable `user.js` blocks Firefox's uninstall for every profile

- **Where:** `cmd/mirrormark/install.go`, `uninstallFirefox()`.
- **What:** a `user.js` that exists but cannot be read stops uninstall
  before it changes anything, for every Firefox profile.
- **Effect:** fails safe: nothing is removed, and the error names the file.
  The user must fix that one file before any profile is uninstalled.
- **Found:** review of Firefox's uninstall, 2026-10-15.
- **Fix:** none needed for safety; optionally carry on with the other
  profiles and report the unreadable one at the end, exiting non-zero.

### The Firefox e2e tests use fixed BiDi ports

- **Where:** `e2e/env.go` (ports 9301 and 9302), `e2e/bidi.go`,
  `launchFirefox()`.
- **What:** each Firefox instance listens for BiDi on a fixed port.
- **Effect:** a stale Firefox from an earlier run, or anything else on the
  port, makes `session.new` fail loudly; it cannot pass falsely.
- **Found:** review of the BiDi driver, 2026-10-15.
- **Fix:** start Firefox with `--remote-debugging-port 0` and read the port
  it picks.

### The BiDi handshake has no deadline

- **Where:** `e2e/bidi.go`, `handshake()`.
- **What:** the TCP dial and the WebSocket upgrade have no deadline;
  `bidiCall` sets one only after the session is up.
- **Effect:** a Firefox that accepts the connection but never answers
  hangs the test until the 60-minute `go test` timeout.
- **Found:** review of the BiDi driver, 2026-10-15.
- **Fix:** dial with a timeout and set a deadline on the connection for the
  handshake. `e2e/bidi.go` could use `ws.Dial` instead, which has deadlines
  and handles control frames; that would also fix the next entry.

### The BiDi client ignores control frames and `id: null` errors

- **Where:** `e2e/bidi.go`, `recv()` and `bidiCall()`.
- **What:** `recv` treats ping and close frames as data, and `bidiCall`
  skips an error reply with `id: null` as if it were an event.
- **Effect:** both end in the 30 s deadline error rather than a clear one;
  neither gives a wrong result.
- **Found:** review of the BiDi driver, 2026-10-15.
- **Fix:** answer pings, return on close, and return an `id: null` error.
  Using `ws.Dial` in `e2e/bidi.go` would handle control frames (see the
  previous entry).

### No round check after Firefox restarts

- **Where:** `e2e/install_test.go`, `TestFirefoxInstallAndBind`, after the
  restart.
- **What:** after Chrome restarts, the test waits for `roundDone`; after
  Firefox restarts, it checks only the hello and the log.
- **Effect:** a coverage gap: a Firefox host that connects but cannot
  finish a round after a restart would not fail this test (the sync tests
  would still catch most such breakage).
- **Found:** review of the Firefox e2e tests, 2026-10-15.
- **Fix:** call `roundDone` after the Firefox restart too.

### `TestFirefoxUninstall`'s `extensions.json` check passes if the entry is absent

- **Where:** `e2e/firefox_test.go`, `TestFirefoxUninstall`.
- **What:** the check fails only if mirrormark's entry is present and
  active. If Firefox never listed it, the check passes vacuously.
- **Effect:** the XPI and prefs checks still carry the test, so a broken
  uninstall is still caught; this one check proves less than it seems.
- **Found:** review of the Firefox e2e tests, 2026-10-15.
- **Fix:** before uninstalling, read `extensions.json` and require an
  active mirrormark entry.

### `TestClosedBrowserCatchesUpOnStart/firefox-firefox` failed once

- **Where:** `e2e/sync_test.go`, `TestClosedBrowserCatchesUpOnStart`, the
  Developer Edition pair.
- **What:** after B restarted, the test timed out after 10 s waiting for B
  to catch up, and printed no connect latency for B, so B's host most
  likely never connected within the wait.
- **Effect:** a spurious red `check-e2e`. The test passed 3 times alone
  straight after, with B connecting about 1.2 s after launch.
- **Found:** a full `make check-e2e` while building `mirrormark serve`
  (native messaging, which this change did not alter), 2026-10-15.
- **Fix:** none yet. A likely cause is Gecko dropping an extension's first
  connection about 10 s after start, as the localhost transport spike saw,
  and the reconnect alarm coming after the wait ends. Look at the host log
  of a failing run before widening the wait.

### `TestEditsPropagate/chrome-chrome` once had a bind not report in

- **Where:** `e2e/sync_test.go`, `TestEditsPropagate`, the Chrome pair;
  the bind in `e2e/env.go`.
- **What:** `install` for chrome-b printed `not bound: Default: mirrormark
  did not report in within 30s of opening the page`.
- **Effect:** a spurious red `check-e2e`. It passed in 3 runs alone straight
  after, and in the next full run.
- **Found:** a full `make check-e2e` while adding the systemd user service,
  2026-10-16. The service does not run in e2e (the image has no user
  manager), and binding still goes through native messaging.
- **Fix:** none yet. Look at the host log and Chrome's tab of a failing run
  before widening the wait.

## Manual checks still to do

- Run `mirrormark install` from the README on a real Linux desktop with
  Google Chrome and real `sudo` (the Docker image makes the system
  directories writable, so the e2e tests never run `sudo`). Check that the
  data piped to `sudo install -D -m 0644 /dev/stdin <target>` gets through
  while `sudo` asks for a password, and that `uninstall`'s `sudo rm` works.
- Run the README's Firefox steps on a real Linux desktop with Firefox
  Developer Edition installed the usual way: a profile that has been used,
  under `~/.config/mozilla/firefox` or `~/.mozilla/firefox`, with
  `LastPlatformDir` from a real install. Run the first `install` with
  Firefox open, then bind a running profile over the desktop's D-Bus.
  Check whether `$XDG_CONFIG_HOME` moves Firefox's profiles root (see the
  `DefaultRoot` entry above). Try an upgrade: replace the binary, run
  `install --browser firefox` again, restart Firefox and check the new
  build runs. Then uninstall after real use. Phase 5 is done when this run
  is recorded.
- On the desktop Brave that showed "This page has been blocked by Brave",
  run the README's Brave steps again: `mirrormark install --browser brave`,
  check the listing, then `--profile` one profile and follow the messages.
  Run it under firejail as well, to see whether firejail hides
  `/opt/brave.com/brave/extensions/`. Firejail support itself comes later,
  after the localhost transport. Phase 7 is done when this run is recorded.
- On the desktop, upgrade from the native-messaging build in Chrome and
  Developer Edition, following the README. Check that the first `install`
  asks for a restart and starts the service (`systemctl --user status
  mirrormark`), the second binds, the popup clears, and a server restart
  (`systemctl --user restart mirrormark`) and a browser restart each
  reconnect. Then run the Brave and LibreWolf steps from
  [`plan-brave-librewolf.md`](docs/rfcs/0001_local-bookmark-sync-host.d/plan-brave-librewolf.md)'s
  "After the last task". Phase 8 is done when these runs are recorded.
