# RFC 0001: Sync bookmark folders between browser profiles through YAML files

- Status: draft
- Author(s): Chris Coetzee, Claude
- Date: 2026-10-14
- Links: none yet. Repository: `github.com/chriscz/mirrormark` (MPL-2.0).

## Problem

People who use more than one browser, such as Chrome for one profile and LibreWolf for another, have no local way to keep a bookmark folder the same in all of them. Each browser's own sync stays inside its family and goes through the vendor's servers. Tools that cross families, such as floccus [R10], need a server or cloud storage in the middle.

## Background

mirrormark is a WebExtension plus a small Go program. The user has fixed these choices: YAML files are the only database and config; conflicts are merged into suffixed copies such as `GitHub (chrome/personal)` and `GitHub (chrome/work)`, never resolved by picking a winner; deleting a bookmark inside a union moves it to a `_deleted` folder; Google Chrome on Linux comes first and the Firefox family follows; everything builds and tests in a Docker image.

This RFC describes the smallest version that works: two or more Chrome profiles on Linux. What it leaves out is listed under [Later](#later).

**The bookmarks API.** Chrome exposes `bookmarks.getTree`, `get`, `create`, `update`, `move`, `remove` and change events [R5] [R6]. Node ids are stable but local to one profile, so mirrormark keeps its own ids. Chrome names its top-level folders by `folderType` (Chrome 134+).

**Native messaging** [R1] [R2]. The extension calls `runtime.connectNative(name)`, and the browser starts the program named in a host manifest file, after checking that the extension's id is listed in it. Messages are length-prefixed UTF-8 JSON, at most 1 MB towards the extension. The browser starts one process per connection and kills it when the connection closes. An open connection keeps a Chrome service worker alive [R3].

## Goals

1. **Unions stay the same.** After sync, every member folder of a union holds the same bookmarks and subfolders with the same titles and URLs.
2. **Several Chrome profiles on Linux**, in a design the Firefox family can join later.
3. **Merge, never drop.** When two profiles change the same thing differently, both versions survive as suffixed copies.
4. **Nothing is lost.** Sync removes a bookmark only after the user removes it from `_deleted`.
5. **Near-immediate.** A change in one running profile shows in another running profile within a few seconds.
6. **Simple to run and install.** One Go binary that embeds the extension. No daemon, no network listener, no account. The YAML files are the whole state, and a person can read them.

## Non-goals

- **Asking which side wins.** The engine reports each conflict so a later mode can ask.
- **Syncing reorders.** A new bookmark takes its position from its source; moving a bookmark within its folder stays local.
- **Editing the bookmark lists in database files by hand** (Section 3).
- **Syncing between computers**, or tabs, history and settings.
- **Store listings** on the Chrome Web Store.

## Proposal

We propose plain native messaging, one host process per browser profile, and YAML files as the only shared state. The host processes never talk to each other; they take turns on the files under one lock.

```
 Chrome (personal)       Chrome (work)          mirrormark CLI
   extension               extension            (install, uninstall, status)
      │ native messaging      │ native messaging        │
 mirrormark host         mirrormark host                │
      └───────── lock, read, write ───┬─────────────────┘
   ~/.config/mirrormark/config.yaml   unions; written by the user
   ~/.local/share/mirrormark/
     profiles/<id>.yaml               one database per profile
     backups/<id>/<date>.yaml
     lock
```

### 1. The host: one process per profile, no daemon

The browser starts `mirrormark host` when its extension connects. The process opens the database file the extension is bound to (Section 5), holds a lock on `profiles/<id>.lock` while it runs so that a copied profile cannot drive the same file twice, and exits when the browser closes the connection.

It runs a **round** on connect, whenever the extension reports a change, and whenever `config.yaml` or another member's database file changes. It notices file changes by comparing modification times once a second (`os.Stat`). A round:

1. takes the global lock (`flock` on `~/.local/share/mirrormark/lock`);
2. reads `config.yaml` and the database file of every profile that shares a union with this one;
3. merges each union (Section 4) and sends this browser the writes it needs;
4. if every write succeeded and the bookmarks changed, writes this profile's database file, setting the `seq` of each union record it changed to one above the highest `seq` of that union it read;
5. releases the lock.

A host writes only its own browser and its own file; other profiles catch up in their next round. If any write fails, or the browser does not answer for 10 seconds, the round writes no file, logs a warning, and the next round starts again from the same file (Section 4, Why it converges). A crashed host releases its locks with it.

If another member's file is missing or unreadable, the round skips every union that member belongs to and reports it in `mirrormark status`. It never treats a member it cannot read as empty.

### 2. The protocol

```jsonc
// extension → host, first message; "db" is set by `mirrormark install` (Section 5)
{"v": 1, "type": "hello", "db": "chrome-personal", "browser": "chrome/141.0"}

// extension → host, after hello, after a burst of bookmark events (0.5 s quiet), after each batch
{"v": 1, "type": "tree", "nodes": [
  {"id": "1", "parent": null, "root": "toolbar"},
  {"id": "57", "parent": "1", "index": 0, "title": "GitHub", "url": "https://github.com/"}]}

// host → extension, at most 1 MB per message
{"v": 1, "type": "apply", "batch": 7, "ops": [
  {"op": "move", "id": "57", "parent": "90", "expect": {"parent": "58"}},
  {"op": "create", "ref": "n1", "parent": "58", "index": 0,
   "title": "GitHub (chrome/work)", "url": "https://github.com/orgs/b"}]}

// extension → host, one result per op
{"v": 1, "type": "applied", "batch": 7, "results": [{"ok": true}, {"ok": true, "id": "212"}]}

// either direction
{"v": 1, "type": "error", "code": "db-mismatch", "message": "…"}
```

- **Whole trees.** The extension always sends its whole tree, and the host compares it with the database file, so a missed event is fixed by the next tree.
- **No blind writes.** `update`, `move` and `remove` carry what the host expects to find. The extension reads the node first and fails the op as `stale` if it differs, so an edit the user makes during a batch survives and the next round sees it as a change. Ops run one at a time, as MDN advises when order matters [R5].
- **Versions.** A message with a different `v` gets `error` `unsupported-version`, and the popup says to restart Chrome. This only happens between upgrading the binary and Chrome loading the new extension.

### 3. The YAML files

**One database file per profile**, `~/.local/share/mirrormark/profiles/<id>.yaml`, created by `mirrormark install` and kept current by the host. It holds the profile's identity and, for each union it is a member of, the contents of its member folder as of its last completed round. Bookmarks outside unions are not recorded.

```yaml
# Written by mirrormark. You may edit the profile block; do not edit below it.
format: 1
profile:
  id: chrome-personal              # stable; config.yaml refers to it
  label: chrome/personal           # goes into conflict suffixes
  browser: chrome
  path: /home/chris/.config/google-chrome/Profile 1
unions:
  work:
    seq: 412                       # order of this union's records across members
    path: toolbar/Work             # the config path this record was made for
    id: "58"                       # browser id of this profile's member folder
    children:
      - {title: GitHub, url: "https://github.com/", id: "57", m: m-b7c3, by: chrome/work}
      - {title: Mail (chrome/personal), suffix: chrome/personal, url: "https://fastmail.com/",
         id: "61", m: m-c9d0, by: chrome/personal}
      - title: _deleted
        id: "90"
        m: m-d001
        children:
          - {title: Old wiki, url: "https://wiki.example/", id: "88", m: m-77aa, by: chrome/work}
```

- `id` is the browser's node id. `m` is mirrormark's id, the same in every profile holding the bookmark. The member folder itself has no `m`: members of one union may sit at different paths.
- `by` is the label of the profile that last changed the node; it names the other side's copy in a conflict.
- `suffix` marks a label mirrormark added to the title.
- `seq` orders one union's records across the member files: the record with the highest `seq` holds that union's latest state. It is kept per union, not per file, because a round can skip one union (Section 1) while it writes another, and the skipped union's record must not look newer than it is.

**One config file**, `~/.config/mirrormark/config.yaml`, written by the user:

```yaml
format: 1
unions:
  work:                              # union name
    chrome-personal: toolbar/Work    # profile id: folder path
    chrome-work: toolbar/Work
  reading:
    chrome-personal: other/Reading
    chrome-work: other/Reading
```

- A path starts with a top-level folder, `toolbar`, `other` or `mobile`, or the title the browser shows, such as `Bookmarks bar`. `\/` escapes a slash in a title. One profile's paths may not nest.
- The first round creates a missing member folder and records its `path` and `id`. After that the host checks that the path still leads to that id. If the folder disappears or leaves the path, the host stops syncing that member and reports it in `status`. A missing member folder never means "everything was deleted". To resume, the user restores the folder or points the config at a new path; a path that differs from the recorded one makes the profile a new member (Section 4, First round).

**File safety.**

- **Atomic writes.** Each write goes to a temporary file in the same directory, is flushed to disk, and is renamed over the old file. A crash leaves the old file or the new one, and a reader never sees half a file.
- **Backups.** Before the first write of each day, the host copies the database file to `backups/<id>/<date>.yaml` and deletes all but the newest 30. Because there is at most one backup a day, a burst of bad rounds cannot rotate away every good copy. Backups are for reading; copying one back over a database file is not a supported recovery.
- **Hand edits.** The host validates `config.yaml` on every read; if it is invalid, nothing syncs, and `status` and the popup show the error. In a database file, only the profile block may be edited. The host does not detect edits below it: a removed line reads as a deletion and goes to `_deleted` like any other (or, inside `_deleted`, as a purge), and a truncated file trips the wrong-tree guard (Section 4).

### 4. Union semantics

**A union holds everything any member holds.** A bookmark or folder under any member folder ends up under every member folder, at the same relative path.

**A round is a three-way merge.** For each union, a round for profile P compares three trees, matched by `m`:

- **base:** the union in P's own database file, which is what P's browser held when P last completed a round;
- **ours:** the union in P's live tree;
- **theirs:** the member record of the union with the highest `seq`, which is the union's latest state. If that is P's own file, theirs equals base.

The changes from base to ours are P's user's edits; the changes from base to theirs are everyone else's since then. Each node has three fields that sync: title, URL and parent. For each field:

| Base → ours  | Base → theirs | Result                                    |
| ------------ | ------------- | ----------------------------------------- |
| unchanged    | unchanged     | nothing                                   |
| changed      | unchanged     | ours; `by` = P's label                    |
| unchanged    | changed       | theirs, written to P's browser            |
| changed      | changed, same | that value                                |
| changed      | changed, different | **conflict**                         |

Changes to different fields combine: a title edit on one side and a URL edit on the other give one bookmark with both, and a bookmark moved to `_deleted` on one side and retitled on the other ends up in `_deleted` with the new title.

**Adds.** A node in ours or theirs but not in base is new. If both sides added a node with the same parent, title and URL (for a folder, parent and title), they are linked as one node, one to one in sibling order. If both added a bookmark with the same parent and title but different URLs, that is a **conflict**. Anything else is added to the other side, and a node new in ours gets a fresh `m`.

**Deletion is a move to `_deleted`.** Each member folder has a `_deleted` subfolder, created on first use. When a node in base is gone from P's member folder, by being removed or by being moved out of the union, the round recreates it, with its subtree, in P's `_deleted` (a node moved out of the union also stays where the user put it) and records the change as a move into `_deleted`, which reaches every member like any other move.

- **Edits** to a bookmark inside `_deleted` sync like any other edit.
- **Restoring** is moving it back out, in any profile.
- **Purging** is removing a bookmark from `_deleted`. It is the one real deletion. Because base remembers the bookmark, a removal on one side is a plain three-way removal: the other members remove it in their next round, however long they have been closed. If the other side edited it meanwhile, the edit wins and the bookmark stays. Removing `_deleted` itself purges its bookmarks; the folder comes back on next use.

**Conflict: both survive, suffixed.** For a bookmark, theirs keeps its `m`, retitled `<title> (<its by>)`; ours becomes a new node beside it with a new `m`, titled `<title> (<P's label>)`. Both then spread like any other change. Because `suffix` marks what mirrormark added, a second conflict replaces the suffix instead of stacking `(a) (b)`, and a title the user typed as `Notes (work)` is never touched. Three profiles whose edits all conflict end with three copies.

| Base                       | Chrome personal                 | Chrome work                  | Every member afterwards                                                                |
| -------------------------- | ------------------------------- | ---------------------------- | -------------------------------------------------------------------------------------- |
| `Work/GitHub → github.com` | URL → `github.com/orgs/a`       | URL → `github.com/orgs/b`    | `Work/GitHub (chrome/personal) → …/orgs/a`, `Work/GitHub (chrome/work) → …/orgs/b`     |
| `Work/GitHub → github.com` | retitled `GH`                   | URL → `github.com/orgs/b`    | `Work/GH → …/orgs/b`                                                                   |
| `Work/News → lwn.net`      | deleted                         | unchanged                    | `Work/_deleted/News → lwn.net`                                                         |
| `Work/News → lwn.net`      | deleted                         | retitled `LWN`               | `Work/_deleted/LWN → lwn.net`                                                          |
| (none)                     | adds `Work/Mail → fastmail.com` | adds `Work/Mail → gmail.com` | `Work/Mail (chrome/personal) → fastmail.com`, `Work/Mail (chrome/work) → gmail.com`    |

**Folders are never copied**, because a copy would duplicate the subtree. For a folder conflict, theirs wins, because it is already in other profiles, and ours is logged (Open question 1). The engine checks the merged tree for cycles (A moves X into Y while B moves Y into X) and undoes the move it is applying in P. When a folder moves to `_deleted` while another profile adds a bookmark inside it, the bookmark goes with it: kept, but out of sight until restored.

**First round.** A new member has no base, so all its nodes and all of theirs count as new: identical bookmarks and same-named folders link up, same-titled bookmarks with different URLs become suffixed copies, and the rest is added to both sides. A first round deletes nothing.

**Why it converges.** Rounds never overlap, because each holds the global lock. A round that writes reads every member file first, so its output contains everything published before it; by induction, the highest-`seq` record of a union contains every published change to it. P's base is a state P published, so it is an ancestor of theirs, which is what a three-way merge needs. A round that fails part-way writes no file, so base stays the last published state; in the next round the writes that did land show up on both sides with the same value (a landed `create` links as an identical add), and the rest is retried. Once users stop editing, each profile's next round brings its browser to the latest state, and a round that changes nothing writes nothing, so the rounds stop.

**Wrong-tree guard.** If a round would move more than half of a member folder's nodes to `_deleted` (and the folder holds more than 5), it writes nothing and answers `error db-mismatch`, shown in the popup and in `status`. This catches a browser that has not finished loading its bookmarks, a reset profile, a database bound to the wrong profile and a truncated file. Without it, each of these would move a whole union into `_deleted` everywhere. If the change is real, the user moves the bookmarks into `_deleted` by hand (a move does not trip the guard), or re-runs `mirrormark install` for that profile, which makes it a new member.

**Room for "ask which side wins".** The engine is one pure function, `merge.Round(base, ours, theirs Tree, u Union) Result`, returning the ops, the new tree and each conflict with both versions. A later mode would hold a conflict instead of copying it.

### 5. Which profile is this?

Neither the extension nor the host can learn which browser profile the extension runs in [R1] [R2], and with the CRX route (Section 6) the same extension runs in every profile. So `mirrormark install` binds it: after creating the profile's database file, it runs

```
google-chrome --profile-directory="Profile 1" "chrome-extension://<id>/bind.html#chrome-personal"
```

which opens a tab in that profile (in the running Chrome if there is one). The page stores the database id in `storage.local`, says so, and closes. The page is not web-accessible, so a web page cannot open it. The extension sends the id in every `hello`; an unbound extension stays idle and its popup says to run `mirrormark install`. If the database file is gone or another host holds its lock (a copied profile), the host answers `error` and the popup says why. Reinstalling the extension clears `storage.local`; running `mirrormark install` again rebinds it.

This works in Google Chrome 155 on Linux, with Chrome running or not, once the extension is loaded in that profile (checked in Docker while planning). The first time a profile opens after the extension was installed, the tab can load before the extension does and shows an error. So `install` confirms each binding: the host records each `hello` in a status file, `install` waits up to 4 seconds for it, and opens the page again, up to 3 times.

### 6. Installing

The binary embeds the extension (`go:embed`). `mirrormark install`:

1. lists Chrome's profiles from `~/.config/google-chrome/Local State` (`profile.info_cache`; `--user-data-dir` picks another data directory);
2. lets the user pick one or more, by number or with `--profile "Profile 1"`; with a single profile it picks that one;
3. unpacks the extension into `~/.local/share/mirrormark/extension/` and packs `mirrormark.crx` beside it;
4. writes two system files, as root: `/usr/share/google-chrome/extensions/<id>.json` naming the CRX and its exact version, and the native host manifest `/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json`, which reaches every Chrome data directory. Branded Chrome ignores `--load-extension` since 137 [R12], but on Linux it installs a CRX named this way without a prompt, in every profile [R11] (both directories checked in the spike). If it cannot write them itself, it runs `sudo mirrormark install-system-files …`, which writes only these two files;
5. for each pick, creates the database file (id `chrome-<profile name>`, label `chrome/<profile name>`, made unique if taken), and `config.yaml` if it is missing;
6. if step 4 changed either system file, asks the user to restart Chrome and run `mirrormark install` again, because Chrome reads the extensions directory only at startup; otherwise it binds each pick (Section 5).

Without `sudo`, it writes the host manifest into `<data dir>/NativeMessagingHosts/` instead, which needs no root, and prints the fallback: open `chrome://extensions` in each picked profile, turn on Developer mode, Load unpacked, choose the unpacked folder; then run `mirrormark install` again to bind. The unpacked manifest carries the install's `key`, so the id is the same.

**A key per install.** Chrome derives an extension's id from its public key. `install` generates a key pair with Go's standard crypto, writes the public key into the unpacked extension's manifest `key`, signs the CRX with the private key, puts the id in the host manifest's `allowed_origins`, and keeps the private key in `~/.local/share/mirrormark/extension.pem` (mode `0600`) so upgrades keep the id. No private key is shipped, so an extension built elsewhere does not get this install's id. An upgrade re-signs with the same key and raises `external_version` so Chrome picks up the new build.

`mirrormark uninstall` removes the host manifests, the preferences file, the unpacked extension and the CRX, and keeps the YAML files and the key unless given `--purge`. Removing the preferences file removes the extension from every Chrome profile at Chrome's next start.

### 7. Extension and code

The extension is thin: Manifest V3, permissions `bookmarks`, `nativeMessaging` and `storage`, plain JavaScript with no build step, so the binary embeds the source files as they are. It connects from top-level service-worker code, registers `runtime.onStartup` so Chrome starts the worker when a profile opens, and reconnects on each bookmark event. It sends trees, applies ops, and shows the bound profile, the last batch and any error in its popup. Besides `install` and `uninstall`, the CLI has `status` (profiles, unions, last rounds, errors, log path) and `host` (what the native host manifest runs; Chrome starts the binary with the extension's origin as its only argument, which also selects host mode).

One Go module, `github.com/chriscz/mirrormark`. Its only third-party dependency is `go.yaml.in/yaml/v3`, the YAML organisation's continuation of the archived `gopkg.in/yaml.v3` [R16]. Locking uses `syscall.Flock`; the CRX3 file is written with the standard library.

As in tab-stash, everything builds and tests in a dev image (`Dockerfile` with pinned Go, Google Chrome stable and Xvfb; `bin/dev run make`), with the `Makefile` as the one build system. The end-to-end tests are Go tests that drive Chrome over the DevTools protocol on `--remote-debugging-pipe`, which needs no WebSocket client and no third-party package.

## Alternatives considered

**Per-node versions** (the previous draft). Each node carried a version `v` and the profile `by` that made it, and a `purged` list with 90-day expiry recorded real deletions. A three-way merge against each profile's own last file gives the same answers with less state: base tells a removal from a node a profile never had, so no purge list is needed and a long-closed profile no longer brings purged bookmarks back; per-field merging replaces whole-node versions, so a title edit and a URL edit no longer conflict. The global lock already gives the total order the versions stood in for, and `seq` records it.

**Telling hosts about changes** through inotify or by waking the other extension. Polling `os.Stat` once a second on a few files costs nothing measurable, needs no dependency, and works the same on every platform. A host cannot wake another profile's extension.

**A checksum over each database file.** It caught hand edits so they were not misread as browser changes. A hand edit can at worst move bookmarks to `_deleted`, the wrong-tree guard catches a truncated file, and the YAML parser catches a corrupt one, so the checksum is dropped.

**Choosing the profile in the popup** (the previous draft). On first connect the host offered the unbound databases and the user picked one. Binding at install time removes the `choose` message, the `claimed` flag and the unclaim step, and the user cannot pick the wrong profile. The spike showed that Chrome opens the bind page from the command line, so the popup pick is not needed.

**TypeScript and Puppeteer.** A `tsc` build and a Node test driver would bring Node and npm into the dev image for a few hundred lines of extension code. Plain JavaScript and a Go DevTools client over a pipe keep one toolchain.

**One daemon behind a native-messaging relay.** Each host process relayed to a long-running `mirrormark serve` over a Unix socket. It needed a daemon that outlives the browser's host and both browsers open at once. With the files standing in for the other members, a round needs only its own browser, and a closed browser catches up when it starts.

**A localhost WebSocket server.** Any local program or web page can reach a localhost port, so it needs tokens, `Origin` checks and DNS-rebinding defences. Native messaging lets the browser vouch for the extension.

**Editing the browsers' own bookmark files.** Chrome rewrites `Bookmarks` from memory, so edits made while it runs are lost.

**One shared file, or SQLite.** A shared file would make every round rewrite every profile's data; SQLite cannot be read by hand. Per-profile files let each host write only its own.

**Real deletes.** A mistaken delete would be final everywhere. `_deleted` keeps deletes visible and reversible.

**floccus with a local WebDAV server** [R10]. Works today, but needs a server and has no suffix merge.

## Tradeoffs and risks

- **`_deleted` grows** in every member until the user empties it.
- **Merge clutter.** Each conflict leaves suffixed copies to tidy.
- **A failing write stalls one profile.** If an op fails every time, that profile never completes a round and never publishes its own edits; `status` and the popup show the warning.
- **One slow browser stalls everyone.** A round holds the global lock while its browser applies ops, up to the 10-second timeout.
- **The CRX route needs root and reaches every profile.** Users who refuse `sudo` get the manual fallback, which is tested by hand only.
- **Installation depends on undocumented files** (`Local State`, the preferences-file directory) that Chrome may change.
- **The guard can refuse real changes.** Removing most of a member folder at once trips it; moving the bookmarks into `_deleted` does not.
- **No export yet.** Recovering from a bad sync means `_deleted`, the YAML backups read by hand, or the browser's own bookmark export made beforehand; `install` suggests making one.
- **Same-user programs are trusted.** They can rewrite the YAML files, as they could the browsers' own files.

## Rollout

Chrome on Linux, end to end. Each phase is its own stack of commits, and nothing writes to a browser before Phase 4.

1. **Dev image, YAML database and merge engine.** `Dockerfile`, `bin/dev`, `Makefile`; the file formats, atomic writes, backups, config validation and `merge.Round`.
   - **Tests:** a table test per row and example in Section 4; property tests over random trees, three members and random round orders (some members closed, some rounds failing part-way) checking convergence, no writes after a quiet round, and no bookmark lost except by purge.
   - **Exit:** `bin/dev run make` passes.
2. **Extension and host.** The extension, the host, the bind page, and writing the database file. **Tests:** a Go fake extension drives the host. **Exit:** passes.
3. **`mirrormark install`.**
   - **Tests:** in the dev image, whose two system directories are writable so `install` needs no `sudo`, `install` runs against Google Chrome stable in two data directories driven over the DevTools pipe, once with fresh profiles and once with used ones.
   - **Exit:** in both runs the extension is enabled and bound, and reaches the host with no user action beyond the restart. The fallback is tried by hand once and recorded in the PR.
4. **Union sync.** Writes, `_deleted`, purges, `status`, the guard. **Tests:** e2e with two Chrome instances in one union covering each example in Section 4, one browser closed while the other changes, and the latency target. **Exit:** all pass, and a run on the developer's Chrome with test profiles is recorded in the PR.

**Rollback.** Remove a member from `config.yaml`, which stops its syncing without touching its bookmarks, or run `mirrormark uninstall`.

## Later

Each is its own RFC or stack once the Chrome version works.

- **Firefox family.** Firefox, LibreWolf and others: profiles from `profiles.ini` (`~/.config/mozilla/firefox/` from Firefox 147 [R15], `~/.mozilla/firefox/`, `~/.librewolf/`), a generated Firefox manifest, XPI signing through `web-ext sign` [R13] (LibreWolf's signature checks: conflicting reports [R14]), Firefox's fixed root ids and its separators, tags and keywords, binding a profile, and LibreWolf's host directory [R8]. Open: whose addons.mozilla.org account signs.
- **Chromium and Brave.** Their profile directories, `--load-extension` [R12] and preferences-file directories.
- **`mirrormark export`** of a database or backup to a bookmarks HTML file both families import.
- **`mirrormark preview <union>`**, printing what a union would become before a member is added.
- **Flatpak and Snap browsers**, which restrict native messaging.
- **Windows and macOS**, which refuse local CRX files [R11].
- **Release packaging**: `make release` and a README tested in a fresh container.

## Success criteria

- Two Chrome profiles with a few hundred bookmarks in one union match within 3 seconds of the last edit while both run (0.5 s quiet, up to 1 s polling, two rounds; measured in Phase 4), and within 3 seconds of a closed one's extension connecting.
- Each example in Section 4 gives the tree shown, in every member.
- In the property tests and the Phase 4 e2e, no bookmark goes missing except by purge, and a round with no user changes writes nothing.
- `mirrormark install` takes a fresh and a used Chrome profile on Linux to a bound extension using only the steps it prints.

## Security, privacy, operational impact

- **Security.** No network listener; only the extension id in the host manifest can start the host [R1] [R2]. The host validates every message and file and executes nothing it receives. The extension has no host permissions. The `sudo` step writes one JSON file naming a CRX in the user's data directory.
- **Privacy.** The YAML files and backups hold the user's bookmarks in plain text, mode `0600`. Nothing leaves the computer, and there is no telemetry. `uninstall --purge` deletes them.
- **Operations.** About 2 MB per database file per 10,000 bookmarks (estimate), times up to 30 backups. The host logs to `~/.local/state/mirrormark/log` and records its last `hello`, round and error in `~/.local/state/mirrormark/<id>.status.yaml`, which `status` and `install` read; `status` prints the log path.

## Open questions

1. **One folder renamed or moved differently on two profiles.** Proposed: theirs wins and ours is logged. Alternative: one folder titled `X (a) / Y (b)`. _Resolve in RFC_; ask the user.
2. **Chrome account and local folders.** A signed-in Chrome profile can have two bookmark bars [R6]. Proposed: paths resolve in the account set when it exists. _Resolve in implementation._
3. **URL matching.** Should `http`/`https` or a trailing slash count as the same URL? Proposed: no. _Resolve in implementation._

## References

External claims were checked on 2026-10-14 unless marked unverified.

- **[R1]** MDN, "Native messaging" and "Native manifests". https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging
- **[R2]** Chrome, "Native messaging". https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging
- **[R3]** Chrome, "The extension service worker lifecycle". https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle
- **[R5]** MDN, `bookmarks`. https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/bookmarks
- **[R6]** Chrome, `chrome.bookmarks`: `folderType` and `syncing` since Chrome 134. https://developer.chrome.com/docs/extensions/reference/api/bookmarks
- **[R8]** LibreWolf host locations (third-party). https://codeberg.org/librewolf/issues/issues/2308
- **[R10]** floccus. https://github.com/floccusaddon/floccus
- **[R11]** Chrome, "Alternative extension installation methods": on Linux a preferences file may name a local CRX (`external_crx`, `external_version`), and "Linux users won't be prompted to enable the extension; it is installed automatically"; Windows (since 33) and macOS (since 44) refuse local CRX files. https://developer.chrome.com/docs/extensions/how-to/distribute/install-extensions
- **[R12]** Chromium extensions group: `--load-extension` removed from branded Chrome 137, kept in Chromium and Chrome for Testing. https://groups.google.com/a/chromium.org/g/chromium-extensions/c/1-g8EFx2BBY/m/S0ET5wPjCAAJ
- **[R13]** Mozilla, add-on signing: `xpinstall.signatures.required` is honoured only on Developer Edition, Nightly, ESR and unbranded builds; unlisted signing with `web-ext sign`. https://support.mozilla.org/en-US/kb/add-on-signing-in-firefox, https://extensionworkshop.com/documentation/publish/signing-and-distribution-overview/ (from a research summary, not re-read for this draft)
- **[R14]** LibreWolf and signature checks (third-party, conflicting). https://issues.guix.gnu.org/76616, https://gitlab.com/librewolf-community/browser/linux/-/issues/293
- **[R15]** Firefox profiles; XDG directories on Linux since Firefox 147. https://firefox-source-docs.mozilla.org/toolkit/profile/index.html
- **[R16]** `go.yaml.in/yaml/v3`, frozen except for security fixes; v4 in development. https://pkg.go.dev/go.yaml.in/yaml/v3, https://github.com/yaml/go-yaml

## History

- 2026-10-14 — draft
- 2026-10-14 — cut to a minimal Chrome-only version: three-way merge replaces per-node versions and the purge list; install-time binding replaces the popup claim; checksum, export and preview dropped or deferred; Firefox moved to Later.
- 2026-10-14 — while planning: database files record only member folders, with `seq` per union record; bind URL and install directories verified in Docker; install runs in two passes around the Chrome restart and confirms each binding; plain JavaScript extension; Go DevTools-pipe e2e driver instead of Puppeteer.
