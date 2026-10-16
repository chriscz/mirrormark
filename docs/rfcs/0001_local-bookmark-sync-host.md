# RFC 0001: Sync bookmark folders between browser profiles through YAML files

- Status: draft
- Author(s): Chris Coetzee, Claude
- Date: 2026-10-14 (Firefox Developer Edition added 2026-10-15; Brave and LibreWolf designed 2026-10-15; explicit profiles and one-shot binding 2026-10-15; a local server instead of native messaging 2026-10-15; a challenge instead of sending the token 2026-10-15)
- Links: none yet. Repository: `github.com/chriscz/mirrormark` (MPL-2.0).

## Problem

People who use more than one browser, such as Chrome for one profile and LibreWolf for another, have no local way to keep a bookmark folder the same in all of them. Each browser's own sync stays inside its family and goes through the vendor's servers. Tools that cross families, such as floccus [R10], need a server or cloud storage in the middle.

## Background

mirrormark is a WebExtension plus a small Go program. The user has fixed these choices: YAML files are the only database and config; conflicts are merged into suffixed copies such as `GitHub (chrome/personal)` and `GitHub (chrome/work)`, never resolved by picking a winner; deleting a bookmark inside a union moves it to a `_deleted` folder; Google Chrome on Linux comes first and the Firefox family follows; everything builds and tests in a Docker image.

This RFC describes the smallest version that works: two or more Chrome profiles on Linux (Phases 1 to 4), then Firefox Developer Edition profiles joining the same unions (Section 8, Phase 5), then Brave and LibreWolf as variants of those two installs (Section 9, Phase 6), then a local server in place of native messaging (Section 11, Phase 8). What it leaves out is listed under [Later](#later).

**The bookmarks API.** Chrome exposes `bookmarks.getTree`, `get`, `create`, `update`, `move`, `remove` and change events [R5] [R6]. Node ids are stable but local to one profile, so mirrormark keeps its own ids. Chrome names its top-level folders by `folderType` (Chrome 134+). Firefox has the same API under `chrome.*`, with fixed ids for its top-level folders instead of `folderType`, and separators [R5].

**The transport** (Section 11). The extension opens a WebSocket to `mirrormark serve` on `127.0.0.1`. That needs no host permission and shows no prompt in Chrome, Brave, Firefox Developer Edition or LibreWolf. Any local program, and some web pages, can reach a local port too, so the server checks each connection's `Origin`, and the server and the extension each prove that they hold a per-profile token without sending it. A message from the server every 20 s keeps a Chrome service worker alive [R3]; a Gecko event page also needs an extension API call on each message. Earlier versions used native messaging [R1] [R2], in which the browser starts one host process per connection; Section 11 says why it went.

## Goals

1. **Unions stay the same.** After sync, every member folder of a union holds the same bookmarks and subfolders with the same titles and URLs.
2. **Several Chrome profiles on Linux**, and Firefox Developer Edition, Brave and LibreWolf profiles in the same unions, in a design other browsers of both families can join later.
3. **Merge, never drop.** When two profiles change the same thing differently, both versions survive as suffixed copies.
4. **Nothing is lost.** Sync removes a bookmark only after the user removes it from `_deleted`.
5. **Near-immediate.** A change in one running profile shows in another running profile within a few seconds.
6. **Simple to run and install.** One Go binary that embeds the extension and runs as one user service, listening only on `127.0.0.1`. No account and no network beyond the computer itself. The YAML files are the whole state, and a person can read them.

## Non-goals

- **Asking which side wins.** The engine reports each conflict so a later mode can ask.
- **Syncing reorders.** A new bookmark takes its position from its source; moving a bookmark within its folder stays local.
- **Editing the bookmark lists in database files by hand** (Section 3).
- **Syncing between computers**, or tabs, history and settings.
- **Store listings** on the Chrome Web Store or addons.mozilla.org, and release Firefox, which needs a signed add-on (Later).

## Proposal

We propose one local server, `mirrormark serve`, that runs one host per browser connection, and YAML files as the only shared state. The hosts never talk to each other; they take turns on the files under one lock.

```
 Chrome (personal)       Chrome (work)          mirrormark CLI
   extension               extension            (install, uninstall, unpack)
      │ WebSocket, 127.0.0.1  │                         │
      └─── mirrormark serve ──┘                         │
           one host per connection                      │
           └────────── lock, read, write ┬──────────────┘
   ~/.config/mirrormark/config.yaml   unions; written by the user
   ~/.local/share/mirrormark/
     profiles/<id>.yaml               one database per profile
     backups/<id>/<date>.yaml
     lock
```

### 1. The host: one per connected profile

`mirrormark serve` runs one host for each extension connection (Section 11). The host opens the database file the extension is bound to (Section 5), holds a lock on `profiles/<id>.lock` while the connection lasts so that a copied profile cannot drive the same file twice, and stops when the connection closes.

It runs a **round** on connect, whenever the extension reports a change, and whenever `config.yaml` or another member's database file changes. It notices file changes by comparing modification times and sizes once a second (`os.Stat`). A round:

1. takes the global lock (`flock` on `~/.local/share/mirrormark/lock`);
2. reads `config.yaml` and the database file of every profile that shares a union with this one;
3. merges each union (Section 4) and sends this browser the writes it needs;
4. if every write succeeded and the bookmarks changed, writes this profile's database file, setting the `seq` of each union record it changed to one above the highest `seq` of that union it read;
5. releases the lock.

A host writes only its own browser and its own file; other profiles catch up in their next round. If any write fails, or the browser does not answer for 10 seconds, the round writes no file, logs a warning, and the next round starts again from the same file (Section 4, Why it converges). A failed round is retried 5 seconds later, or sooner when a file changes, until a round succeeds, so a browser that keeps failing does not take the global lock, or add to the log, every second. If the browser made every change but the host cannot write its file, the round reports `db-write-failed` and is retried the same way. A crashed server releases every lock with it, and systemd starts it again (Section 11).

If another member's file is missing or unreadable, the round skips every union that member belongs to, logs a warning and sends it to the popup. It never treats a member it cannot read as empty.

### 2. The protocol

```jsonc
// extension → host, first message; "db" is set by `mirrormark install` (Section 5), "nonce" is new each time (Section 11)
{"v": 1, "type": "hello", "db": "chrome-personal", "nonce": "9a4e…", "browser": "chrome/141.0"}   // or "firefox/159.0"

// host → extension, only if it has that database: its nonce and its proof that it holds the token
{"v": 1, "type": "challenge", "nonce": "c0d7…", "proof": "51b2…"}

// extension → host, only after it checked the host's proof; the host runs no round until this is right
{"v": 1, "type": "proof", "proof": "e83a…"}

// extension → host, at the first bookmark event of a burst; the host starts no round until the next tree
{"v": 1, "type": "changed"}

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

// host → extension, at the end of every round; no problems when it synced cleanly
{"v": 1, "type": "status", "problems": [{"code": "db-mismatch", "message": "…"}]}

// host → extension, every 20 s, so the browser keeps the extension running (Section 11)
{"v": 1, "type": "ping"}

// either direction; from the host, only when it stops
{"v": 1, "type": "error", "code": "db-locked", "message": "…"}
```

- **Whole trees.** The extension always sends its whole tree, and the host compares it with the database file, so a missed event is fixed by the next tree.
- **No rounds on an old tree.** While the extension waits for quiet, the host's copy of the tree lacks the user's latest edits, and a round that a file change starts then could create again what the user just made, such as the member folder. So the extension sends `changed` at once, and the host's poll waits for the tree that follows.
- **No blind writes.** `update`, `move` and `remove` carry what the host expects to find. The extension reads the node first and fails the op as `stale` if it differs, so an edit the user makes during a batch survives and the next round sees it as a change. Ops run one at a time, as MDN advises when order matters [R5], and the extension stops at the first op that fails and answers the rest `not run`: each op assumes the ones before it landed. A `remove` takes one bookmark or one empty folder (`bookmarks.remove`, never `removeTree`), and the host removes a folder's contents before the folder, so a folder that still holds something fails to go instead of taking it along.
- **Problems.** The popup shows the last `status` message's problems, so a skipped union or an invalid config stays visible until a round no longer finds it. The host's `error` codes are `unsupported-version`, `bad-db`, `bad-token` (the connection's origin is not the database's, the database has no token, or the extension's proof is wrong, Section 11), `db-missing`, `db-locked` and `db-lock-error` (the lock file cannot be locked at all); the `status` codes are `config-invalid`, `db-missing`, `union-skipped`, `db-mismatch`, `apply-failed`, `db-write-failed` and `db-lock-error`.
- **Frames.** Each message is one WebSocket text frame (Section 11).
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

- A path starts with a top-level folder, `toolbar`, `other` or `mobile`, or the title the browser shows, such as `Bookmarks bar`. A Firefox member may also start with `menu`, Firefox's Bookmarks Menu (Section 8). `\/` escapes a slash in a title. One profile's paths may not nest.
- The first round creates a missing member folder and records its `path` and `id`. After that the host checks that the path still leads to that id. If the folder disappears or leaves the path, the host stops syncing that member, logs it and sends it to the popup. A missing member folder never means "everything was deleted". To resume, the user restores the folder or points the config at a new path; a path that differs from the recorded one makes the profile a new member (Section 4, First round).

**File safety.**

- **Atomic writes.** Each write goes to a temporary file in the same directory, is flushed to disk, and is renamed over the old file. A crash leaves the old file or the new one, and a reader never sees half a file.
- **Backups.** Before the first write of each day, the host copies the database file to `backups/<id>/<date>.yaml` and deletes all but the newest 30. Because there is at most one backup a day, a burst of bad rounds cannot rotate away every good copy. Backups are for reading; copying one back over a database file is not a supported recovery.
- **Hand edits.** The host validates `config.yaml` on every read; if it is invalid, nothing syncs, and the log and the popup show the error. In a database file, only the profile block may be edited. The host does not detect edits below it: a removed line reads as a deletion and goes to `_deleted` like any other (or, inside `_deleted`, as a purge), and a truncated file trips the wrong-tree guard (Section 4).

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

**Folders are never copied**, because a copy would duplicate the subtree. For a folder conflict, theirs wins, because it is already in other profiles, and ours is logged (Open question 1, awaiting the user's sign-off). The engine checks the merged tree for cycles (A moves X into Y while B moves Y into X) and undoes the move it is applying in P. When a folder moves to `_deleted` while another profile adds a bookmark inside it, the bookmark goes with it: kept, but out of sight until restored.

**First round.** A new member has no base, so all its nodes and all of theirs count as new: identical bookmarks and same-named folders link up, same-titled bookmarks with different URLs become suffixed copies, and the rest is added to both sides. A first round deletes nothing.

**Why it converges.** Rounds never overlap, because each holds the global lock. A round that writes reads every member file first, so its output contains everything published before it; by induction, the highest-`seq` record of a union contains every published change to it. P's base is a state P published, so it is an ancestor of theirs, which is what a three-way merge needs. A round that fails part-way writes no file, so base stays the last published state; in the next round the writes that did land show up on both sides with the same value (a landed `create` links as an identical add), and the rest is retried. Once users stop editing, each profile's next round brings its browser to the latest state, and a round that changes nothing writes nothing, so the rounds stop.

**Wrong-tree guard.** When P's base holds more than 5 nodes, the round writes nothing and reports `db-mismatch` in its `status`, shown in the popup and the log, if any of these holds:

- P's member folder is empty: a browser that has not finished loading its bookmarks, or a reset profile.
- More than half of base is gone from P's member folder, and more than half of the member folder is not in base: a database bound to the wrong profile.
- More than half of base would go. A node gone from P's member folder, purges included, counts only where its parent is still there, so deleting a folder counts once however much it holds; deleting it is an ordinary edit. A node live in base and gone from theirs counts each time: theirs lacks it only if it was purged or the file was truncated or cut by hand, and counting it is safe because the global lock means theirs is always a whole published state.

Without the guard, each of these would move a whole union into `_deleted`, or purge it, everywhere. If the change is real, the user moves the bookmarks into `_deleted` by hand (a move does not trip the guard), or puts them in one folder and deletes it, or moves the profile's database file aside and re-runs `mirrormark install` for that profile, which makes it a new member (a re-run alone keeps the existing file).

**Room for "ask which side wins".** The engine is one pure function, `merge.Round(merge.Input) (merge.Result, error)`. The input holds base, ours, theirs and P's label; the result holds the ops, the new tree and each conflict with both versions, and the error is the wrong-tree guard's. A later mode would hold a conflict instead of copying it.

### 5. Which profile is this?

The extension cannot learn which browser profile it runs in, and with the CRX route (Section 6) the same extension, with the same `Origin`, runs in every profile. So `mirrormark install` binds it: after creating the profile's database file, it runs

```
google-chrome --profile-directory="Profile 1" "chrome-extension://<id>/bind.html#chrome-personal.<token>"
```

which opens a tab in that profile (in the running Chrome if there is one). The page stores the database id and the token (Section 11) in `storage.local`, says so, and closes. The page is not web-accessible, so a web page cannot open it. The extension sends the database id in every `hello` and proves the binding with the token, which it never sends; an unbound extension stays idle and its popup says to run `mirrormark install`. If the database file is gone or another host holds its lock (a copied profile), the host answers `error` and the popup says why. Reinstalling the extension clears `storage.local`; running `mirrormark install` again rebinds it.

This works in Google Chrome 155 on Linux, with Chrome running or not, once the extension is loaded in that profile (checked in Docker while planning). Before the extension is loaded, the tab shows an error page instead ("This page has been blocked by Chrome"). So `install` first checks the profile's own files and opens the page only in a profile where they say the extension is installed and enabled. It opens the page once and waits up to 30 seconds for the extension to report in with the database id; the host touches `profiles/<id>.lock` each time a connection proves the binding, and `install` watches its modification time. It does not retry (Section 10).

### 6. Installing

The binary embeds the extension (`go:embed`). `mirrormark install` (for Chrome, the default `--browser chrome`; Firefox is Section 8):

1. lists Chrome's profiles from `~/.config/google-chrome/Local State` (`profile.info_cache`; `--user-data-dir` picks another data directory). Without `--profile` it prints them with their state and stops, having written nothing (Section 10);
2. binds the profiles named with `--profile`, by ID (`"Profile 1"`) or by number from the list (repeatable). Binding a profile only creates its database file; nothing syncs until `config.yaml` names it;
3. unpacks the extension into `~/.local/share/mirrormark/extension/` and packs `mirrormark.crx` beside it;
4. writes one system file, as root: `/usr/share/google-chrome/extensions/<id>.json` naming the CRX and its exact version. Branded Chrome ignores `--load-extension` since 137 [R12], but on Linux it installs a CRX named this way without a prompt, in every profile [R11] (checked in the spike). If it cannot write the file itself, it copies it into place with `sudo install -D -m 0644`, so the root step runs no mirrormark code. It leaves a native host manifest an earlier mirrormark left for `uninstall` to remove, and writes and starts the user service (Section 11);
5. for each profile it binds, creates the database file (id `chrome-<profile name>`, label `chrome/<profile name>`, made unique if taken), and `config.yaml` if it is missing;
6. if step 4 changed the system file, asks the user to restart Chrome and run `mirrormark install` again, because Chrome reads the extensions directory only at startup; otherwise it checks each named profile and binds those where the extension is ready (Sections 5 and 10).

Without `sudo`, `install` stops and says so; a no-root route is listed under [Later](#later). `mirrormark unpack [DIR]` does step 3 alone, into `DIR` if given, and prints the folder, for Load unpacked while developing or in a profile where the user removed the extension (Chrome does not reinstall it there). The unpacked manifest carries the install's `key`, so the id is the same as the installed one.

**A key per install.** Chrome derives an extension's id from its public key. `install` generates a key pair with Go's standard crypto, writes the public key into the unpacked extension's manifest `key`, signs the CRX with the private key, records `chrome-extension://<id>` as the `origin` of each database it binds (Section 11), and keeps the private key in `~/.local/share/mirrormark/extension.pem` (mode `0600`) so upgrades keep the id. No private key is shipped, so an extension built elsewhere does not get this install's id. An upgrade re-signs with the same key and raises `external_version` so Chrome picks up the new build.

`mirrormark uninstall` removes the preferences file, the unpacked extension, the CRX and any host manifest an earlier mirrormark left, and the user service if no other browser still uses it (Section 11), and keeps the YAML files and the key unless given `--purge`. `--purge` deletes Chrome's databases, their backups and the key, and the config, log and data directory only if no Firefox profile's database is left, since that browser's profiles still sync through them and its `uninstall` finds its profiles through their databases. A host manifest that names another extension belongs to another install, so `uninstall` leaves it in place. Removing the preferences file removes the extension from every Chrome profile at Chrome's next start.

### 7. Extension and code

The extension is thin: Manifest V3, permissions `alarms`, `bookmarks` and `storage` and a content security policy that lets Gecko open `ws://127.0.0.1` (Section 11), plain JavaScript with no build step, so the binary embeds the source files as they are. It connects from top-level service-worker code, registers `runtime.onStartup` so Chrome starts the worker when a profile opens, and reconnects on each bookmark event. After a failed connection it waits instead for an alarm (30 s, or 60 s after the host's `error`), because the worker's timers stop with it, and a periodic one-minute alarm starts a worker the browser stopped (Section 11); while the reconnect alarm is pending neither a bookmark event nor a restarted worker reconnects; after a new binding it waits a second so the old host can release the database. It sends `changed` at the first bookmark event of a burst and the tree after it, applies ops, and shows in its popup the bound profile, why the host stopped if it did, and the last round's problems. Besides `install` and `uninstall`, the CLI has `unpack` (above) and `serve` (the server, Section 11).

One Go module, `github.com/chriscz/mirrormark`. Its only third-party dependency is `go.yaml.in/yaml/v3`, the YAML organisation's continuation of the archived `gopkg.in/yaml.v3` [R16]. Locking uses `syscall.Flock`; the CRX3 file is written with the standard library.

As in tab-stash, everything builds and tests in a dev image (`Dockerfile` with pinned Go, Google Chrome stable and Xvfb, and, from Phase 5, Firefox Developer Edition and D-Bus; `bin/dev run make`), with the `Makefile` as the one build system. The end-to-end tests are Go tests that drive Chrome over the DevTools protocol on `--remote-debugging-pipe`, which needs no WebSocket client and no third-party package. From Phase 5 they also drive Firefox over WebDriver BiDi with a standard-library client, in a D-Bus session.

### 8. Firefox Developer Edition

A Firefox profile is a member like a Chrome profile: one host per profile, the same files, protocol and merge, and unions that mix both browsers. What differs is the extension's edges, installing and binding. Each point below was checked in the Firefox spike (`0001_local-bookmark-sync-host.d/firefox-install-spike.md`) on Developer Edition 159.0b2 in Docker unless it says otherwise.

**Only Developer Edition.** Release Firefox (158.0 in the spike) installs only add-ons signed by Mozilla and ignores `xpinstall.signatures.required`; Developer Edition honours it [R13]. `install` reads the profile's Firefox from its `compatibility.ini` (`LastPlatformDir`) and the channel from `<LastPlatformDir>/defaults/pref/channel-prefs.js`, and refuses any profile whose channel is not `aurora` (Developer Edition), saying that release Firefox needs a signed add-on, which is Later. A profile that has never run has no `compatibility.ini`; `install` asks the user to start Firefox in it once. Nightly and ESR also honour the pref [R13] but were not tried, so they are refused too.

**One extension.** `manifest.json` gains `"scripts": ["background.js"]` beside `service_worker`, and `browser_specific_settings.gecko` with the id `mirrormark@chriscz.github.io` and `strict_min_version` 128.0. Firefox runs the script as an MV3 event page; Chrome ignores both keys (no warnings in Chrome for Testing 155). `background.js` changes in five places, none of which changes what it does in Chrome:

- it listens only to the bookmark events that exist (Firefox lacks `onChildrenReordered`, `onImportBegan` and `onImportEnded`, and listening to a missing event stops the script);
- it names Firefox's top-level folders by id: `toolbar_____` → `toolbar`, `unfiled_____` → `other`, `mobile______` → `mobile`, `menu________` → `menu`;
- it hides separators (below);
- it labels itself `firefox/<version>` from the user agent;
- it read a native port's disconnect reason from the port's `error` as well as `runtime.lastError` (gone with the native port, Section 11).

An open native port kept a Firefox event page alive (160 s checked). A WebSocket does not, so the extension makes an extension API call on each ping (Section 11).

**Separators.** Firefox has separators (`type: "separator"`); Chrome has none. The extension never sends them to the host, and a node's `index` in the tree counts only the siblings that are not separators. When it applies a `create` or `move` with an index, it maps that index to the real one: the position of the index-th sibling that is not a separator, or the end. Order does not sync (Non-goals), so a separator can end up between different neighbours than before; mirrormark never moves, edits or copies one. The one exception: Firefox, like Chrome, refuses to remove a folder that is not empty, and the host removes a folder only once everything it knows inside is gone, so before a `remove` of a folder that holds only separators, the extension removes them. This happens only in a purge, and a separator holds nothing. A folder that holds anything else keeps its separators, and its `remove` fails as it would without them.

**Same-parent moves.** Firefox reads a move's index within the same parent as the final position; Chrome counts it before the node leaves its old place. The merge never sends such a move (reordering stays local), so the extension does not convert between the two; a merge that starts sending them must add the conversion.

**The `menu` folder.** Paths may start with `menu` (or `Bookmarks Menu`), but only for Firefox members. A Chrome member whose path starts with `menu` has its union skipped (`union-skipped`, no top-level folder `menu`), as for any top-level folder a browser lacks; the other members go on. Firefox has no account folders, so `syncing` never applies.

**Ids and labels.** A Firefox profile's database id is `firefox-<slug of its name>` and its label `firefox/<slug>`; `browser` in its profile block is `firefox`. Firefox's node ids are 12-character GUIDs rather than small integers.

**The host.** Firefox's connections reach the same server, with the `Origin` `moz-extension://<uuid>`, which differs per profile (Section 11). Nothing else in the host changes; framing and the 1 MB limit towards the extension are the same.

**Installing**, without root. `mirrormark install --browser firefox [--user-data-dir ROOT] [--profile NAME]...`:

1. lists the profiles, and without `--profile` prints them and stops, as for Chrome (Section 10): `ROOT` is `--user-data-dir`, else `$XDG_CONFIG_HOME/mozilla/firefox` (`~/.config/mozilla/firefox` when that is unset) if it holds `profiles.ini` (Firefox 147 and later [R15]), else `~/.mozilla/firefox`. It reads `profiles.ini` and adds every `ROOT/*/` that holds a `prefs.js` and is not listed, named by its directory after the first `.`; Firefox's new profile switcher records its profiles only in an SQLite file, which the standard library cannot read;
2. picks the profiles named with `--profile` (the profile directory's base name, or its number in the list), and refuses any that is not Developer Edition or has never run, as above, before writing anything;
3. creates each profile's database file, and `config.yaml` if missing, as for Chrome, before it writes into any profile, so a write that fails part way never leaves a profile with its checks off and no database for `uninstall` to find it by;
4. writes, as the user, in each picked profile `extensions/mirrormark@chriscz.github.io.xpi`, packed from the embedded files like the CRX, and three lines in `user.js`: `xpinstall.signatures.required` false, `extensions.autoDisableScopes` 14 and `extensions.startupScanScopes` 1. A line `uninstall` reset is turned back where it is, and only prefs still missing are appended, so install and uninstall cycles add no lines. A `user.js` that is a symlink is written where it points, and an existing one keeps its mode, so a file kept with the user's dotfiles or made private stays so. On its next start Firefox installs the add-on enabled with no prompt; 14 clears only the profile bit of Firefox's default 15, so add-ons sideloaded into the user, application or system scopes still start disabled and ask. It leaves a host manifest an earlier mirrormark left in `~/.mozilla/native-messaging-hosts/` for `uninstall` to remove, and writes and starts the user service (Section 11);
5. if it changed any of those files, asks the user to restart Firefox in those profiles if it is running and to run `install` again; otherwise it checks each profile and binds those where the add-on is ready.

**Binding.** The extension's pages are at `moz-extension://<uuid>/`, with a UUID Firefox picks per profile and keeps in `prefs.js` (`extensions.webextensions.uuids`). Pinning it in `user.js` would replace every add-on's UUID and lose their storage, so `install` reads it. If it is missing, or `extensions.json` does not show the add-on active, the profile is not ready, and `install` says what to do instead of starting Firefox itself (Section 10). For a ready profile it records `moz-extension://<uuid>` as the database's `origin` (Section 11), then runs `<LastPlatformDir>/firefox --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>` once, which opens the page in that profile whether Firefox runs there or not (a running one gets it over D-Bus), and waits for the extension to report in, as for Chrome (Section 5). `about:blank` is there because `bind.html` closes its tab, and closing the only tab of the only window quits Firefox.

**Uninstalling.** `mirrormark uninstall --browser firefox` removes the XPI from each profile whose database file says `browser: firefox`, sets `xpinstall.signatures.required` back to true and `extensions.autoDisableScopes` back to 15 (their defaults) in its `user.js`, removes a host manifest an earlier mirrormark left in `~/.mozilla/native-messaging-hosts/` if it names the gecko id, and removes the user service if no other browser still uses it (Section 11). It leaves `extensions.startupScanScopes` at 1, which only makes Firefox look in the profile's own `extensions` folder at start, so that it notices the XPI is gone. The reset applies to every line that sets those prefs to mirrormark's values, including lines the user wrote, which only turns the checks back on. It writes the `user.js` files before it removes anything, so if a write fails, running `uninstall` again finishes the job, and it leaves a `user.js` with nothing to change untouched. If no database names a Firefox profile, it changes no `user.js` and says so, naming the two prefs to check by hand. It keeps the YAML files unless given `--purge`, which deletes the Firefox profiles' databases and their backups, and the config, log and data directory only if no Chrome profile's database is left.

**Decisions taken for the user.** The user was away when Section 8 was written; each of these is the smallest safe choice and awaits sign-off:

- separators are hidden from the host and never touched, except inside a folder being purged, and indexes are mapped around them;
- same-parent moves, which the merge never sends, are not converted between the browsers' meanings;
- `menu` is allowed in paths of Firefox members only;
- profiles come from `profiles.ini` plus a scan for `prefs.js`, without reading SQLite;
- profiles of release (or any channel other than Developer Edition) Firefox are refused, not half-installed;
- binding reuses `bind.html` and the lock-file confirmation, with the UUID read from `prefs.js`;
- `uninstall` puts the two safety prefs back.

### 9. Brave and LibreWolf

Brave installs as Chrome does, and LibreWolf as Firefox Developer Edition does. We propose no new install route: each package gets a small description of each browser it supports, and the existing install, bind and uninstall code reads its paths, names and checks from it. Each point below was checked in Docker in the Brave spike (`0001_local-bookmark-sync-host.d/brave-install-spike.md`, Brave 1.97.57 on Chromium 155) or the LibreWolf spike (`librewolf-install-spike.md`, LibreWolf 158.0-1 from the official tarball) unless it says otherwise.

**One description per browser.** Only these values differ between the browsers of one family:

| | Chrome | Brave | Firefox Developer Edition | LibreWolf |
| --- | --- | --- | --- | --- |
| `--browser`, `browser:` | `chrome` | `brave` | `firefox` | `librewolf` |
| Profiles | `$XDG_CONFIG_HOME/google-chrome` (default `~/.config/…`) | `$XDG_CONFIG_HOME/BraveSoftware/Brave-Browser` | `$XDG_CONFIG_HOME/mozilla/firefox` if it holds `profiles.ini`, else `~/.mozilla/firefox` | `$XDG_CONFIG_HOME/librewolf/librewolf` if it holds `profiles.ini`, else `~/.librewolf` |
| Binary | `google-chrome` | `brave-browser` | `<LastPlatformDir>/firefox` | `<LastPlatformDir>/librewolf` |
| External extension file | `/usr/share/google-chrome/extensions/<id>.json` | `/opt/brave.com/brave/extensions/<id>.json` | | |
| Recognised by | | | channel `aurora` in `channel-prefs.js` | `Name=LibreWolf` in `<LastPlatformDir>/application.ini` |

Everything else is shared within a family: Chrome and Brave share the key, the CRX and the extension id; Developer Edition and LibreWolf share the XPI, the three `user.js` lines and their resets, the UUID lookup and the bind URL. Until Section 11 each family also shared a native host manifest.

**Brave.** Brave reads external extension files from its executable's directory, `/opt/brave.com/brave/extensions/`. The extension id depends only on the key, so both browsers connect with one `Origin`. Brave installs the CRX enabled and without a prompt, in existing profiles and in profiles created later. Its `Local State` has the same `profile.info_cache` as Chrome's, its profiles keep the extension's state in `Preferences` as Chrome's do, and binding runs `brave-browser` with Chrome's arguments, once, in a profile whose files say the extension is ready (Section 10). Brave's user agent is Chrome's, so the hello says `chrome/155.0.0.0`; the host does not need to tell the two apart, because the database id carries the profile's identity. `/usr/share/chromium/extensions/` also works in Brave but reaches Chromium too, so we use Brave's own directory.

**Shared Chromium files.** `install --browser brave` packs the same CRX and writes Brave's external file, with `sudo` as for Chrome. `install` writes only its own browser's external file, never the other's, so it creates nothing under a browser that is not installed. After an upgrade the user runs `install` once per browser, as for Chrome and Firefox today; a browser whose `install` was not run again keeps its old build. `uninstall --browser brave` removes Brave's external file. It removes the CRX and the unpacked extension only if no other Chromium browser's external file is still in place, and otherwise says which browser still uses them. `--purge` deletes Brave's databases and backups, and the key only if no other Chromium browser has its external file in place or a database left; the same rules apply to Chrome with Brave as the other browser.

**What a Brave or Chrome user sees.** Both browsers show a "Safety Check" card on their extensions page saying they cannot verify where mirrormark comes from (branded Chrome 155 shows it for the same install); the extension stays on. Brave restores the last session at start, where Chrome opens a new tab, so a `bind.html` tab left from an open that did not confirm opens again at the next start and binds the profile then. It holds the database id that `install` created for that profile, so this binds to the right database.

**LibreWolf.** LibreWolf is a release build, but it is built without `MOZ_REQUIRE_SIGNING` and honours `xpinstall.signatures.required`, which settles the conflicting reports [R14] for 158.0-1; none of the three prefs is locked, and their defaults are Firefox's (`true`, 15), so uninstall's resets are right. The Developer Edition route works unchanged: the XPI in the profile's `extensions` folder, the three `user.js` lines, the UUID from `prefs.js` (LibreWolf's own order of native host manifest directories [R8] no longer matters since Section 11), and `<bin> --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>`, cold or into a running LibreWolf over D-Bus. Its `channel-prefs.js` says `release`, so the channel cannot tell it from release Firefox; `application.ini`'s `Name=LibreWolf` can. Its user agent stays `Firefox/158.0` under `resistFingerprinting`, so its hello says `firefox/158.0`. LibreWolf clears cookies and cache at shutdown by default, but the binding in `storage.local` survived clean restarts.

**Which Gecko profiles install accepts.** The Developer Edition-only gate becomes "the browser asked for": `--browser firefox` takes only profiles whose browser is on the `aurora` channel, `--browser librewolf` only profiles whose browser's `application.ini` says `Name=LibreWolf`. Any other Gecko browser, release Firefox among them, is still refused before anything is written, and the message names the two that work.

**Shared Gecko files.** Since Section 11, Developer Edition and LibreWolf share no file: `uninstall --browser librewolf` removes the XPI and resets the prefs in LibreWolf's profiles only. All four browsers share the user service, which goes with the last browser that still has its own file in place (Section 11). As for Chromium, it is the files still in place that decide, not the databases, which an uninstall without `--purge` leaves behind.

**Profiles that have never run.** A first LibreWolf start creates two profiles, and the second, `default`, never runs, so it has no `compatibility.ini` and `install` cannot tell which browser it belongs to. The list shows it as `never run`, and naming it with `--profile` is refused before anything is written. Since Section 10, nothing is installed by default, so there is no default set to skip it from. The same code serves Developer Edition, so this applies there too.

**Ids and labels.** A Brave profile's database id is `brave-<slug>` and its label `brave/<slug>`, so a conflict copy reads `GitHub (brave/personal)`; a LibreWolf profile's are `librewolf-<slug>` and `librewolf/<slug>`. `browser` in the profile block is `brave` or `librewolf`. `mirrormark install` and `uninstall` accept `--browser chrome`, `brave`, `firefox` or `librewolf`, and `--purge` works per browser as before.

**Not supported.** Only the packagings the spikes ran are supported: Brave stable from Brave's `.deb` (`/opt/brave.com/brave`) and LibreWolf from its official tarball. Brave Beta and Nightly have other binaries and data directories; Flatpak and Snap browsers were not tried (Later). LibreWolf from its Debian/Ubuntu repository, AppImage or AUR runs from another directory; `install` may well work with it, since it reads the directory from `compatibility.ini`, but nobody has tried. The README lists these as untested.

**Tested end to end.** The e2e suite installs and binds a Brave profile beside Chrome's and a LibreWolf profile beside Developer Edition's, on Section 11's server, and checks that uninstalling Brave or LibreWolf keeps what Chrome or Developer Edition still use. The sync tests run a fourth pair, Brave and LibreWolf, beside Chrome and Chrome, Firefox and Firefox, and Firefox and Chrome. It is one cross-engine pair, since the others would add little beyond what their families' pairs show and each pair adds minutes. The same 3 s latency target holds for it.

**Decisions taken for the user.** The user was away when this section was written; each of these is the smallest safe choice and awaits sign-off:

- Brave and LibreWolf are descriptions in the existing Chrome and Firefox packages, not new install paths;
- Brave shares Chrome's key, CRX and id (and, until Section 11, the system host manifest), and writes its external file to `/opt/brave.com/brave/extensions/`;
- shared files go with the last browser that still has its own file in place (the external file for Chromium, the XPI for Gecko); the Chromium key also stays while another Chromium browser's databases remain;
- `install` writes only the browser it was asked for; after an upgrade it is run once per browser;
- databases are `browser: brave` / `librewolf`, ids `brave-<slug>` / `librewolf-<slug>`, labels `brave/<slug>` / `librewolf/<slug>`; the host does not tell browsers apart from the hello;
- LibreWolf takes the Developer Edition route unchanged, recognised by `application.ini`, and every other Gecko browser is still refused before anything is written;
- profiles that have never run are listed as such and refused when named, in Developer Edition too (this replaced "skipped with a note unless named" once Section 10 made `--profile` required);
- the e2e harness keeps `remote.prefs.recommended` false: the remote agent's recommended prefs set `extensions.autoDisableScopes` to 0, which would hide whether install's prefs work.

### 10. Choosing profiles, and binding once

The user tried the Brave install on a desktop. The bind page said "This page has been blocked by Brave", and mirrormark was not listed in `brave://extensions` at all, while `install` opened the page again and again. The profile-state spike (`0001_local-bookmark-sync-host.d/profile-state-spike.md`) explains the page. Chrome and Brave show it for a `chrome-extension://` URL whose extension the profile has not loaded. A Chromium browser reads the external file only at start; it never picked one up while running, even 40 seconds later. So the extension was not installed in that profile yet, and no number of retries would have helped. The spike also shows that each profile records the extension's state in a file `install` can read. This section uses those findings.

**`install` changes nothing unless profiles are named.** `mirrormark install [--browser B] [--user-data-dir DIR]` with no `--profile` prints B's profiles and exits 0. It is a query, like listing branches, and the user's next step is to pick from the list. It writes nothing: no key, extension, CRX, database, config or system file, and it starts no `sudo`. It still refuses root, a Chromium browser whose command is not on `PATH` (`google-chrome` or `brave-browser`), and an unknown `--browser`, as before. For example:

```
Brave profiles in /home/u/.config/BraveSoftware/Brave-Browser:
  #  ID          NAME      STATE          DATABASE
  0  Default     Personal  bound          brave-personal
  1  Profile 1   Work      ready          -
  2  Profile 2   Test      not installed  -
  3  Profile 10  Old       removed        brave-old
Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:
  mirrormark install --browser brave --profile 'Profile 1'
```

The example is the first profile that is not `bound` (number 0 if all are), by its ID, in single quotes when the shell would split or expand it.

**IDs and numbers.** A profile's ID is its directory's name: `Default` or `Profile 1` in Chrome and Brave, and the base name of the profile directory in Developer Edition and LibreWolf (such as `x1y2z3ab.default-release`). The list is sorted by ID, comparing runs of digits as numbers: `Default`, `Profile 1`, `Profile 2`, `Profile 10`. A profile's number `#` is its place in that list, from 0. In a Chromium browser where no profile was deleted, `Profile N` is then number N, so a user who confuses the two still gets the profile they meant. Numbers stay the same while the set of profiles does; IDs never change. `--profile` (repeatable) takes either. An exact ID wins over a number, and no Chromium ID is all digits. An ID that two Gecko profiles share (two absolute paths with one base name) is refused with both numbers. Names are no longer accepted, because they need not be unique and the list shows them anyway.

**States.** The STATE column, and the reason `install` gives for a profile it does not bind, come from the profile's own files:

| State | Chrome and Brave (`<profile>/Preferences`) | Developer Edition and LibreWolf |
| --- | --- | --- |
| `never run` | no `Preferences` | no `compatibility.ini` |
| `not <browser>` | | the check in Sections 8 and 9 refuses the profile: `compatibility.ini` names another browser, or it, its `LastPlatformDir`, or the browser's `channel-prefs.js` or `application.ini` is missing or cannot be read. The reason goes to stderr as a warning |
| `not installed` | no `extensions.settings.<id>`, and no external file for this browser | no XPI in the profile |
| `not installed yet` | no `extensions.settings.<id>`, and this browser's external file is in place | the XPI is there, but `extensions.json` has no entry or `prefs.js` no UUID |
| `removed` | `<id>` in `extensions.external_uninstalls`, and no `extensions.settings.<id>` with a `path` (a copy loaded unpacked after a Remove has a `path` again, and the list may keep the id) | (Gecko deletes the XPI and keeps no record: `not installed`) |
| `turned off` | `disable_reasons` not empty | `userDisabled` true |
| `refused` | | `appDisabled` true (for example, signatures required again) |
| `old build <version>` | `manifest.version` is not the embedded extension's | `version` is not the embedded extension's |
| `ready` | an entry with a `path`, no disable reason, this version | entry `active`, a UUID, this version |
| `bound` | `ready`, and a database names this profile | the same |
| `unreadable` | `Preferences` cannot be read or parsed, or mirrormark's own entry in it cannot (another extension's entry is not read); the error goes to stderr as a warning | `extensions.json` or `prefs.js` cannot be read or parsed, or the XPI cannot be checked; the same |

Without a key in mirrormark's data directory, the extension id is unknown, and every Chromium profile reads `not installed`, or `never run` if it has no `Preferences`.

**Binding once, when ready.** With `--profile`, `install` gates and writes as before: the Gecko browser check before any write, the databases before any profile write, the system or profile files, and the restart line if it changed them. Otherwise it reads each named profile's state. A profile that is not `ready` or `bound` gets one line naming the state and what to do, and nothing is opened in it. For example: not installed yet, so quit Brave completely and start it again, open the profile, wait half a minute, and run the command again; turned off, so turn it on in `brave://extensions`; removed, which Brave will not undo, so run `mirrormark unpack ~/mirrormark-extension` and load that folder with Load unpacked (not the folder `unpack` uses by default, which `install` rewrites). For each ready profile, `install` opens the bind page once and waits up to 30 seconds for the extension to report in with the profile's database id. There are no retries anywhere. The race the earlier spikes saw, a tab that loaded before the extension was installed, cannot happen now: `install` opens the page only once the profile's files show the extension. A cold start, a slow LibreWolf, and a worker that has to start all fit within 30 seconds. `install` exits 1 if any named profile was not ready or did not report in. `install` no longer starts a Gecko browser to learn the UUID. Each command these lines, the restart line and the "not running" line give is the whole command to type: `--user-data-dir` as the user gave it, and every profile named, by ID. A named Chromium profile that has never run is not refused: it gets its database and the system file, then `not ready: … has never opened this profile`, while a named Gecko profile that has never run is refused before anything is written, since the browser check needs its `compatibility.ini`.

The files can lag a running Chromium browser by up to about 17 seconds (Tradeoffs and risks), so the not-ready messages say to wait and run the command again. They do not say the extension is missing for certain.

**User decisions.** The user made these after the desktop run:

- bind once, when ready: `install` reads the profile's files to confirm the extension is installed and enabled there before it opens anything, then opens the bind page exactly once with one bounded wait. Profiles that are not ready are listed with a precise reason and what to do. No retries;
- explicit profiles: `install` without `--profile` lists the profiles with their number, ID, name and state, and changes nothing. `--profile` takes an ID or a number, numbered in ID order with digits compared as numbers. Matching by name is dropped. The never-run skip is no longer needed, and a named never-run profile is still refused (as built, a Chromium one is told to open once instead; see above);
- everything already built for safety stays: the Gecko browser check before any write, databases before profile writes, the Chromium `PATH` check, shared files kept while another browser uses them, and `--purge` per browser.

Chosen by us within those decisions: exit 0 for the list, numbers from 0, a 30-second wait, and the state names above.

### 11. A local server instead of native messaging

Native messaging makes the browser start the host, so a browser in a sandbox (firejail, Flatpak, Snap) starts it inside that sandbox, where it needs holes to reach mirrormark's files. The user decided to drop it: the extension now connects over a WebSocket to one long-running `mirrormark serve` on `127.0.0.1`. The localhost transport spike (`0001_local-bookmark-sync-host.d/localhost-transport-spike.md`) ran this in Chrome 155, Brave 1.97.57, Developer Edition 159 and LibreWolf 158 in Docker; each point below comes from it unless it says otherwise. Firejail itself is a later step (Later); this section only keeps the host out of the browser's sandbox.

**`mirrormark serve`.** One server per user. It first takes `serve.lock` in the data directory without waiting, so a second `serve` exits with "mirrormark is already running". It then listens on `127.0.0.1` on the first of the ports `61959`, `63018` and `63454` it can bind, and exits with an error naming the three if it can bind none. The ports lie above Linux's ephemeral range (32768–60999), so an outgoing connection never holds one by chance. They are written once, in `extension/ports.json`: the binary reads that file from its embedded copy, and the extension reads its own copy at start, so the two cannot disagree. The server logs to `~/.local/state/mirrormark/log`, as the host did, and tags each line with the connection's number (`conn=`).

**One host per connection.** The server runs the per-connection logic of Sections 1 and 2 unchanged, one goroutine per connection, over a connection that reads and writes messages instead of stdin and stdout. Nothing about rounds changes. The global lock still keeps rounds apart: `flock` locks belong to the open file, so goroutines in one process exclude each other as separate processes did. A second connection for a database already in use still gets `db-locked`, as a copied profile did. Each host still polls the files once a second. When a connection closes, its host stops and releases its database's lock.

**Which database a connection is: a challenge, not the token.** The token never crosses the socket. Instead each side proves it holds it, and the server proves it first, so the extension sends nothing that helps anyone until it knows it reached its own mirrormark. On each connection:

1. The extension sends `{"v": 1, "type": "hello", "db": <id>, "nonce": <nonce_e>, "browser": …}`. The database id is not secret.
2. The host answers only if the database file exists, its profile block's `origin` equals the connection's `Origin` header, it has a `token` of 64 lowercase hex digits (the server logs a malformed one), and `nonce_e` is well formed. It then sends `{"v": 1, "type": "challenge", "nonce": <nonce_s>, "proof": <proof_s>}`, with `proof_s = HMAC-SHA256(token, "server:" + port + ":" + nonce_e)`. Otherwise it answers `error` and stops: `db-missing` as before, or the new code `bad-token`. The peer has proven nothing yet, so a `db-missing` names no file and no error; the log has those details. In both cases the popup says to run `mirrormark install` for this profile again.
3. The extension computes `proof_s` itself and compares. If it differs, the extension closes that socket, sends nothing more on it, and tries the next port (below).
4. The extension sends `{"v": 1, "type": "proof", "proof": <proof_e>}`, with `proof_e = HMAC-SHA256(token, "extension:" + nonce_s)`, then its tree.
5. The host compares `proof_e` in constant time (`hmac.Equal`). If it differs, it answers `error` `bad-token` and stops. Only when it matches does it take the database's lock and touch the lock file, so `db.Connected` (Section 5) still confirms a binding, and only for a proven one. Messages before then, other than `hello` and `proof`, are ignored, as a tree before a hello is today. Until then a message may be at most 4 KB, enough for a hello or a proof; a larger one closes the connection with 1009 before any of it is read. The host reads the next message only once it has handled the last, so the tree the extension sends straight after its proof is read with the usual 64 MB limit. A connection has 10 seconds from the upgrade to a right `proof_e`. One that says nothing, or never answers the challenge, is closed then without touching any file, so a program cannot hold connections open for ever without proving anything; nor can it hold a keep-alive HTTP connection that only got refused (above).

Encodings, exactly: each nonce is 32 bytes from a cryptographic random source, written as 64 lowercase hex digits. The HMAC key is the token as text, its 64 lowercase hex digits encoded as ASCII (not the 32 bytes they stand for), so neither side decodes hex. The HMAC input is the ASCII string shown, with `port` the decimal port the server listens on and the nonce as its 64 hex digits; each proof is the 32-byte HMAC as 64 lowercase hex digits. The extension uses Web Crypto (`crypto.subtle.importKey("raw", …, {name: "HMAC", hash: "SHA-256"})` and `sign`); the server uses `crypto/hmac` and `crypto/sha256`.

- **The labels and the port stop relays.** `server:` and `extension:` keep a proof made by one side from passing as the other's. The port in `proof_s` stops a program holding one port from relaying the extension's hello to the real server on another port and passing back its proof: the real server's proof names its own port, which is not the one the extension dialled, and two programs cannot listen on one port of `127.0.0.1` (Linux shares a port between sockets only for one user).
- **Fresh nonces stop replays.** A `proof_e` seen before is for an old `nonce_s` and fails against the new one, and a `proof_s` seen before is for an old `nonce_e`.
- **The extension's comparison need not be constant-time.** A timing leak would at most help an attacker guess the right `proof_s` for one `nonce_e` byte by byte, over many attempts, but each connection gets one guess and a new `nonce_e`. The server, which answers any number of connections, uses `hmac.Equal`.

What a program that answers on a port learns: the database id, a nonce it cannot use, and the browser's name and version. It cannot compute `proof_s`, so the extension sends it nothing more and moves to the next port; it never sees `proof_e` or the token. A program that can read the user's database files has the tokens and can do everything: pose as the server to the extension, or as any profile to the server, and so read and rewrite the bookmarks. It could already rewrite the YAML files and the browsers' own files, so this does not move the boundary, which is the user's own account (Tradeoffs and risks).

**The threat model.** By the user's decision, mirrormark targets a single-user desktop:

- Programs running as the user are trusted. They can already read and write the YAML files, the tokens in them, the log and the browsers' own files.
- Web pages are not trusted. A browser sets a page's `Origin`, and no page can send an extension's, so the `Origin` check stops them.
- The token and the challenge stop a stale binding (a profile bound to another database, or bound again since) and a server that is not the user's mirrormark, such as an old build or another program on a port.
- Other local users are out of scope, as for the token in the bind URL below. Where this section says what such a user's program could do, it says so only to show what would happen, not as something mirrormark defends against.

**Who may connect.** Any local program can open a connection and send any `Origin`. Web pages can connect too, with their own origin: pages from `127.0.0.1` in every browser, pages on a private address in Chrome, Brave and Developer Edition, and an HTTPS page on a public address once the user allows Chrome's Local Network Access prompt. So the server checks two things:

- **Before the upgrade:** the `Host` header must be `127.0.0.1:<port>`, which stops DNS rebinding, and the `Origin` must start with `chrome-extension://` or `moz-extension://` and be the `origin` of some database; the prefix is checked first, so a web page costs no file reads. Anything else gets HTTP 403, and no message is read. This stops every web page. Each refusal is logged at Debug, and at Info at most once a minute with how many came since, so a page that keeps knocking does not grow the log. The server reads at most 8 KB of headers, and closes a keep-alive connection that stays idle for 10 seconds.
- **In the challenge:** the exact origin of the database named, and proof of its token, as above. The proof stops a stale binding and a program that is not mirrormark. In Chrome and Brave, every profile of one install connects with the same origin, `chrome-extension://<id>`, so only the token tells their profiles apart. In Developer Edition and LibreWolf, the origin `moz-extension://<uuid>` is already per profile.

Until a connection proves itself, the server does not know it is mirrormark, so nothing the peer sent goes into the log above Debug: not a header, not the database id from its hello. The host logs its own lines about such a connection at Debug, and the server sums up each dropped one as it does a refusal, at Debug every time and at Info at most once a minute, with a count and a fixed reason (`bad-token`, `db-missing`, `bad handshake`, no proof in time, and so on). Once the proof is right, the host's lines are warnings again.

**Binding hands over the token.** For each profile it binds, `install` first writes two fields into the profile block, under the global lock so that no round overwrites them: `origin`, which is `chrome-extension://<id>` for Chrome and Brave and `moz-extension://<uuid>` (the UUID from `prefs.js`) for Developer Edition and LibreWolf; and `token`, 32 random bytes in hex, generated if the block has none yet. Running `install` again keeps the token and updates the origin, since Gecko gives a reinstalled add-on a new UUID. The bind page's URL becomes `bind.html#<db>.<token>`; the page stores both and the time. The extension sends the id in every hello and uses the token only to compute proofs. The confirmation is unchanged: `install` waits for the lock file's modification time. The token passes through the browser's command line and may stay in its history or restored session, which only the user can read, like the database file. While the browser starts, the command line is also visible to other local users through `ps` or `/proc/<pid>/cmdline`, unless `/proc` is mounted with `hidepid=2`; other local users are out of scope (the threat model above), so by the user's decision this is documented, not designed away.

```yaml
profile:
  id: chrome-personal
  label: chrome/personal
  browser: chrome
  path: /home/chris/.config/google-chrome/Profile 1
  origin: chrome-extension://abcdefghijklmnopabcdefghijklmnop   # written by install
  token: 3f9c…                                                    # 64 hex digits, written by install
```

**The protocol over WebSocket.** The messages of Section 2 stay as they are, one JSON message per WebSocket text frame, with `v` still 1. The hello gains `nonce`, and `challenge`, `proof` and `ping` are new, but no peer of one transport ever meets one of the other: the new extension only dials the server, and the old one only uses native messaging, which the new binary drops. So a version check would never fire. The server sends `{"v": 1, "type": "ping"}` every 20 s on each connection. Messages to the extension stay within 1 MB, so the batch limits do not change; messages from it may be up to 64 MB, as before.

**A WebSocket server in the standard library.** A package of about 250 lines, `internal/ws`, does only what the server needs: the opening handshake (version 13; no subprotocols, extensions or compression), masked frames from the client (text, continuation, ping, which it answers with pong, pong, which it ignores, and close, which it answers), and unmasked, unfragmented text and close frames to the client. It refuses binary frames, unmasked frames, and messages over the limit. No new dependency.

**The extension.**

- **Connecting.** It tries `ws://127.0.0.1:<port>/` on each port in order and keeps the first socket whose server proves it holds the token. A port where nothing listens fails at once, and so does one whose server refuses the upgrade (such as a mirrormark that has no database for this origin). A port whose server answers with a wrong proof, an `error`, or nothing within 5 s is closed too, and the next is tried, so a program on the first port cannot stop sync while the real server listens on a later one. Until a server has proven itself, any program could have answered, so the popup shows only fixed texts that name the port, never words the peer sent: each error a server sends before the proof (`bad-token`, `db-missing`, `bad-db`, `unsupported-version`) has its own text, a wrong proof reads "the program on port N could not prove it is your mirrormark", and anything else "unexpected answer on port N". If no port proves itself, the popup shows the first known error (and the reconnect waits 60 s, as after any host error), else the first wrong proof or unexpected answer; a known error goes first so that a program on one port cannot hide why the real server refused the profile. A profile with a database id but no token, bound by a build before the server, dials nothing, and the popup says it is not bound yet and to run `install` for it again. If nothing answered at all, the popup says "mirrormark is not running. Start it with `systemctl --user start mirrormark`, or run `mirrormark serve`.", and the reconnect alarm is set.
- **Staying alive.** In Chrome and Brave, the server's ping every 20 s keeps the service worker running. In Gecko, socket traffic does not count, so the extension calls `runtime.getPlatformInfo()` on each ping. It does this in every browser, so there is one code path.
- **Reconnecting.** The one-shot alarm set when the socket closes stays (30 s, or 60 s after the host's `error`). A periodic one-minute alarm is added: a browser that stops the background with no traffic closes the socket without running its `onclose`, and only an alarm starts it again.
- **Manifest.** The permission `nativeMessaging` goes, no host permission is added, and `"content_security_policy": {"extension_pages": "script-src 'self'"}` replaces Gecko's default policy. That default includes `upgrade-insecure-requests`, which turns `ws://127.0.0.1` into `wss://` and fails the socket. The version rises to 0.2.0, so every browser picks up the new build.

**The service.** `install --profile …` writes `$XDG_CONFIG_HOME/systemd/user/mirrormark.service` (`~/.config/systemd/user/` when unset) and enables it:

```ini
[Unit]
Description=mirrormark bookmark sync

[Service]
ExecStart=/home/chris/bin/mirrormark serve
Restart=on-failure

[Install]
WantedBy=default.target
```

`ExecStart` is the binary's absolute path, with symlinks resolved, as the host manifest's was. The unit sets no environment: systemd's user manager supplies it, and a user who sets the XDG variables only in a shell must set them for systemd too (`~/.config/environment.d/`), or the server looks for its files elsewhere and each popup reports `db-missing`; the README says so. `install` runs `systemctl --user daemon-reload`, `enable mirrormark.service` and `restart mirrormark.service` on every run with `--profile`, so the server always runs the binary just installed. If `systemctl` is missing or fails, as without a user session bus, `install` says so and prints `mirrormark serve` as the command to keep running. Before it opens any bind page, `install` checks that a server holds `serve.lock`, waiting up to 5 s. If none does, it says how to start one, opens nothing and exits 1. The last `uninstall --purge` deletes `serve.lock` only if it can take it; a server started by hand still holds it, and then the purge keeps the file, so no second server starts beside the first, and says to stop that server. `uninstall --browser B` stops, disables and removes the unit only if no other browser, of either family, still has mirrormark in place (another Chromium browser's external file, or the XPI in a Gecko profile that a database names); otherwise it says which browser still uses it. A user may run `mirrormark serve` by hand instead; `serve.lock` keeps the two from running together.

**Installing, per browser.**

- **Chrome and Brave.** `install` writes only the browser's external file, and `sudo` is needed only for that file. The CRX and the unpacked extension go with the last Chromium browser's external file, and the key rule is unchanged.
- **Developer Edition and LibreWolf.** `install` writes only the XPI and `user.js` in each profile. The two browsers no longer share a file.
- **Old host manifests.** `install` leaves a native host manifest from an earlier mirrormark in place, since nothing starts it any more: the new extension has no `nativeMessaging` permission, and the new binary has no host mode. `uninstall` removes it, even while another browser still uses mirrormark, since no browser needs it: `/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json` if it names this install's extension id (with `sudo rm` if needed), and `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json` if it names the gecko id. One that names another extension belongs to another install and stays.

**Upgrading from the native-messaging build.** The user installs the new binary and runs `install --browser B --profile …` for each browser, as after any upgrade. The extension's version changed, so this is a first pass:

1. It rewrites the external file or the XPIs.
2. It writes and starts the service, and asks for a browser restart.

Until the restart, the old extension cannot reach a host, since the new binary has none. After it, the new extension has a database id but no token yet, so it dials nothing, and the popup says the profile is not bound yet and to run `install` for it again. The second `install` binds each ready profile with its token. Database files without `origin` and `token` load as before. An older binary refuses a file that has them, because it rejects unknown keys, so going back means deleting those two lines first.

**Tests.**

- **Unit tests:** the WebSocket codec; the server's checks (wrong `Host`, unknown `Origin`, a database with no token or another origin, which get an `error` and no challenge); the challenge (the server's proof names its port and the extension's nonce; a wrong `proof_e`, another database's, and a `proof_e` replayed from an earlier connection against a new `nonce_s` are refused with `bad-token` and do not count as reporting in; the right one runs the host); two connections for one database (`db-locked`); the ping; falling back to the next port when the first is taken; `serve.lock`; `install` writing `origin` and `token`, keeping the token when run again, and the bind URL; the unit file and the `systemctl` calls, with a fake `systemctl`, and the message when it fails; uninstall's rule for the unit; uninstall removing an old host manifest that names this install, and keeping one that names another.
- **End-to-end:** the harness starts `mirrormark serve` itself for each test, not through systemd, since the dev image has no user session, and stops it at cleanup. The existing tests keep their meaning: "a new host for a" and "the old host exits" become a new connection for a and the old connection's close, read from the log's connection numbers. One new test stops the server, sees the popup say mirrormark is not running, then puts a program on the first port that accepts any upgrade and answers every hello with a wrong proof, starts the server again (which takes the second port), and sees the profile sync through it while the program on the first port receives a hello but never a `proof`. That is the extension refusing a wrong `proof_s`, which only a browser can test. Phase 6's Brave and LibreWolf end-to-end tests, which had not run yet, run on the new transport.

**User decisions.** The user made these after the spike:

- a long-running `mirrormark serve`, run as a systemd user service that `install` writes and enables and `uninstall` removes; users may also run it by hand;
- native messaging goes entirely: no host manifests, no host mode, no native port. When the server cannot be reached, the popup says mirrormark is not running and how to start it;
- three fixed ports, `61959`, `63018` and `63454`, in both the binary and the extension. The server listens on the first it can bind; the extension tries all three. No port setting yet;
- `127.0.0.1` only. A connection is accepted only with the bound profile's exact extension origin and proof of its token, which `install` generates, stores in that profile's database file, and hands to the extension once, through the bind page;
- a mutual challenge, decided after the first draft (Open question 5): the token never crosses the socket, and the server proves it holds the token before the extension proves it does or trusts the server with anything;
- everything Section 10 and earlier built for safety stays: explicit `--profile`, the bind page opened once and only where the extension is ready, per-browser purge, the Developer Edition and LibreWolf gate, and databases before profile writes;
- firejail is a later, separate step;
- the threat model, decided after the final review: a single-user desktop, where programs running as the user are trusted; the `Origin` check, the token and the challenge stop web pages, stale bindings and servers that are not the user's mirrormark; other local users are out of scope;
- the CLI says what it does and where things are, decided after the final review: `mirrormark help`, and the usage printed on an error, explain how to share folders (a `config.yaml` example, where the profile ids come from, the path rule, and that changes are picked up within seconds without a restart) and list each file where it is for this user, from the same XDG lookup the commands use; after binding every profile named, `install` prints a `config.yaml` to paste with the ids it just bound and says where mirrormark logs; and `install` says that it set up `mirrormark serve` as a systemd user service, where the unit is, that it runs as the user and starts at login, whether it started the server or restarted a running one (whose profiles then reconnect within a minute), and the commands to see its status, read its log and stop it;
- each change to `config.yaml` that the server loads shows in the log once, with the number of unions, so the user can see when an edit took effect.

Chosen by us within those decisions:

- the ports live in `extension/ports.json`;
- `serve.lock` keeps one server per user;
- `origin` and `token` live in the profile block;
- the bind fragment is `<db>.<token>`;
- the ping is a JSON text message, not a WebSocket control frame, because the extension never sees control frames;
- every browser makes the keep-alive call;
- the reconnect alarm becomes periodic as well as one-shot;
- `install` restarts the service on every run with `--profile`;
- the unit sets no environment;
- `install` leaves old host manifests to `uninstall`;
- the protocol version stays 1;
- the new error code is `bad-token`;
- the challenge's encodings: hex nonces and proofs, the token's text as the HMAC key, and the inputs `server:<port>:<nonce_e>` and `extension:<nonce_s>`;
- the port in the server's proof, which stops a relay through another port;
- a port that fails the challenge is skipped like one where nothing listens;
- before a connection's proof, its failures are logged at Debug and summed up at Info once a minute, like refusals, never in the peer's words; the server reads at most 8 KB of headers and closes an idle keep-alive connection after the hello wait, 10 s;
- the hosts of one server share only the last `config.yaml` they logged as loaded, so a change shows once however many profiles are connected; a server that starts logs the file once too.

**Not verified.**

- Lifetimes beyond 9 minutes, sleep and resume, and a server that is down when the browser starts. The periodic alarm should cover the last, but only a server restart while the browser runs was tried. The e2e suite now runs the real server in all four browsers, and a Gecko keep-alive for the length of its tests (the longest, the restart test, takes under two minutes); no longer lifetime has been run.
- Chrome and Brave with their own sandbox: the spike and the dev image run them with `--no-sandbox`.
- Release Firefox, and Firefox's Local Network Access prompt. The `Origin` check refuses such pages whatever the prompt does.
- Why Gecko drops its first socket about 10 s after the browser starts. The reconnect alarm covers it, at the cost of up to 30 s at start. No e2e test failed because of it, and none had to wait longer.

## Alternatives considered

**Per-node versions** (the previous draft). Each node carried a version `v` and the profile `by` that made it, and a `purged` list with 90-day expiry recorded real deletions. A three-way merge against each profile's own last file gives the same answers with less state: base tells a removal from a node a profile never had, so no purge list is needed and a long-closed profile no longer brings purged bookmarks back; per-field merging replaces whole-node versions, so a title edit and a URL edit no longer conflict. The global lock already gives the total order the versions stood in for, and `seq` records it.

**Telling hosts about changes** through inotify or by waking the other extension. Polling `os.Stat` once a second on a few files costs nothing measurable, needs no dependency, and works the same on every platform. A host cannot wake another profile's extension.

**A checksum over each database file.** It caught hand edits so they were not misread as browser changes. A hand edit can at worst move bookmarks to `_deleted`, the wrong-tree guard catches a truncated file, and the YAML parser catches a corrupt one, so the checksum is dropped.

**Choosing the profile in the popup** (the previous draft). On first connect the host offered the unbound databases and the user picked one. Binding at install time removes the `choose` message, the `claimed` flag and the unclaim step, and the user cannot pick the wrong profile. The spike showed that Chrome opens the bind page from the command line, so the popup pick is not needed.

**TypeScript and Puppeteer.** A `tsc` build and a Node test driver would bring Node and npm into the dev image for a few hundred lines of extension code. Plain JavaScript and a Go DevTools client over a pipe keep one toolchain.

**One daemon behind a native-messaging relay.** Each host process relayed to a long-running `mirrormark serve` over a Unix socket. With the files standing in for the other members, a round needs only its own browser, so the relay added a process and a socket for nothing. Section 11 does run a long-running server, but the browser connects to it directly.

**Native messaging** (the design before Section 11). The browser vouches for the extension and starts one host per connection, so there is no listener to defend. The user dropped it because the browser starts the host inside its own sandbox (firejail, Flatpak, Snap), and each sandbox would need holes for the host to reach mirrormark's files; a server outside the sandbox needs only loopback. The price is Section 11's `Host` and `Origin` checks and its challenge.

**`fetch` to the server instead of a WebSocket.** It needs the host permission `http://127.0.0.1/*` in all four browsers, which shows at install, and polling or long requests to get messages from the server. A WebSocket needs no permission.

**Signing the XPI now** through addons.mozilla.org (unlisted, `web-ext sign` [R13]). It would let release Firefox run mirrormark, but needs an account, an API key and a signing step per release, and the question of whose account signs is open. Developer Edition proves the design first; signing is Later.

**Installing into Firefox with `policies.json`.** It works, but it needs root, reaches every profile of every user and marks Firefox as managed. The per-profile XPI needs none of that.

**Pinning the extension's UUID in `user.js`.** It would make bind URLs known in advance, but the pref holds every add-on's UUID, so setting it gives other add-ons new UUIDs and loses their storage.

**Separate install code for Brave and LibreWolf.** Copying the Chrome and Firefox paths would let each browser drift, and the spikes found nothing in either browser that the existing code does not already do with other paths and names. A description per browser keeps one install, one bind and one uninstall per family.

**Brave's file in `/usr/share/chromium/extensions/`.** Brave reads it too, and it sits outside Brave's package tree, but Chromium reads it as well, so it would install mirrormark into a browser the user did not ask for.

**Telling Brave from Chrome in the hello.** Brave reports Chrome's user agent, and the host has no use for the difference: the database id says which profile connected. `install` knows the browser and writes it into the database file.

**Editing the browsers' own bookmark files.** Chrome rewrites `Bookmarks` from memory, so edits made while it runs are lost.

**One shared file, or SQLite.** A shared file would make every round rewrite every profile's data; SQLite cannot be read by hand. Per-profile files let each host write only its own.

**Real deletes.** A mistaken delete would be final everywhere. `_deleted` keeps deletes visible and reversible.

**floccus with a local WebDAV server** [R10]. Works today, but needs a server and has no suffix merge.

## Tradeoffs and risks

- **`_deleted` grows** in every member until the user empties it.
- **Merge clutter.** Each conflict leaves suffixed copies to tidy.
- **A failing write stalls one profile.** If an op fails every time, that profile never completes a round and never publishes its own edits; the log and the popup show the warning. The profile retries every 5 seconds, so its own edits wait up to that long once the cause is fixed, unless a file change starts a round sooner.
- **One slow browser stalls everyone.** A round holds the global lock while its browser applies ops, up to the 10-second timeout.
- **The CRX route needs root and reaches every profile.** Users who refuse `sudo` cannot install this version.
- **`install` opens a tab in every profile it binds**, once per profile, and only in the profiles named with `--profile`.
- **The profile's files can lag the browser.** Chrome and Brave write `Preferences` up to about 17 seconds after the extension is installed, enabled or disabled (profile-state spike). If `install` runs too soon after a restart, it says the profile is not ready and asks the user to run it again. If it runs just after the user turns the extension off, it can still read it as enabled, open the page once, and report `not bound`. Neither case writes anything wrong.
- **Installation depends on undocumented files** (`Local State`, a profile's `Preferences`, the preferences-file directory) that Chrome may change. If `extensions.settings` moved, every profile would read `not installed` and `install` would bind none; it would not bind a wrong one.
- **The guard can refuse real changes.** Removing more than half of a union one node at a time, or emptying the member folder, trips it; deleting one folder that holds as much, or moving the bookmarks into `_deleted`, does not. Emptying a `_deleted` that holds more than half of the union node by node trips it too; removing the `_deleted` folder itself does not. A profile that was closed while another one moved most of the union to `_deleted` and purged it sees the guard every round, and must move its database file aside and re-run `install` to become a new member.
- **No export yet.** Recovering from a bad sync means `_deleted`, the YAML backups read by hand, or the browser's own bookmark export made beforehand; `install` suggests making one.
- **Unsigned add-ons in Firefox profiles.** `install` sets `xpinstall.signatures.required` to false in each Firefox profile it binds, so that profile accepts any unsigned add-on, not only mirrormark's, and with `extensions.autoDisableScopes` 14 an add-on file dropped into its `extensions` folder starts enabled without asking. Add-ons sideloaded into the other scopes, such as `~/.mozilla/extensions`, still start disabled and ask, as by default. A program that can write the profile directory could already change the profile in other ways, but the user loses Mozilla's signature check there. The README says so before the user runs `install`; `uninstall` restores both prefs.
- **Only Developer Edition.** Release Firefox, which most people use, needs a signed XPI. Until then Firefox support reaches only people who run Developer Edition.
- **Firefox upgrades are unchecked.** An upgrade replaces the XPI in each profile; that Firefox picks up a changed XPI at its next start (with `startupScanScopes` 1) was not checked in the spike, and Phase 5's e2e does not check it either. If Firefox kept the old build it would go on running it, and the host refuses an old build only when the protocol version changed (`unsupported-version` in the log). An upgrade from the native-messaging build depends on it: a Firefox that kept the old build would keep trying native messaging and never sync until the add-on is replaced.
- **Undocumented Firefox files.** `profiles.ini`, `compatibility.ini`, `channel-prefs.js` and the UUID map in `prefs.js` are not documented interfaces. The new profile switcher's profiles are found by scanning for `prefs.js`, and get the directory's name rather than the one Firefox shows.
- **URLs Firefox refuses.** A Chrome bookmark whose URL Firefox will not store (perhaps `chrome://` or `file:` URLs; not checked) fails its `create` in Firefox every round, which stalls that Firefox profile like any failing write. Phase 5 does not check which schemes Firefox refuses; the stalled profile logs the failing op.
- **Brave's file lives in Brave's package tree.** Whether a Brave upgrade or removal leaves `/opt/brave.com/brave/extensions/` alone was not checked. If an upgrade removed the file, Brave would remove mirrormark at its next start, and running `install --browser brave` again would put it back.
- **Brave's own features were not tried with mirrormark:** Brave Sync (whether it changes `syncing` or the bookmark ids), the policy route, and CDP with the default data directory.
- **Upgrades are per browser.** Every Chromium external file names the one CRX and its exact version. A user who upgrades and runs `install` for Chrome but not for Brave leaves Brave on the old build (or, if Brave had not yet installed it, failing to install the new CRX) until `install --browser brave` runs; if the protocol changed, the popup and the log say `unsupported-version`.
- **A restored bind tab from an older install.** If the user moved a Brave profile's database aside and installed again, a failed tab from before could open at the next start and bind the old id; the popup then reports `db-missing`, and running `install` again fixes it.
- **LibreWolf's future and other packagings.** A later LibreWolf could turn on `MOZ_REQUIRE_SIGNING` or lock one of the three prefs; `install` would still write them, and the add-on would not load (`not bound`). Only the tarball in `/opt/librewolf` and LibreWolf's default sanitize settings were run; a user who makes LibreWolf clear more at shutdown may lose the binding at each restart, which the popup reports as unbound.
- **The bind URL shows the token in process listings.** On Linux, other local users can read the browser's command line, and so the token, with `ps` or `/proc/<pid>/cmdline` unless `/proc` is mounted with `hidepid=2`. The user decided to document this, not change the design.
- **Same-user programs are trusted.** They can rewrite the YAML files, as they could the browsers' own files, and read the tokens in them, so they could also talk to the server as any profile, or answer the extension as its server.
- **Sync needs the server running.** If the service is stopped, or keeps failing until systemd gives up, nothing syncs, and each popup says mirrormark is not running. Bookmarks stay as they are; the next round catches up.
- **Another program can take the ports.** A program that holds `61959` before mirrormark starts, such as an unrelated server, receives each extension's hello: the database id, a one-time nonce and the browser's version. It cannot answer the challenge, so it learns nothing usable, and the extension moves on to the real server on the next port. A program that holds all three ports stops sync for that user; the popup says the program on the first port could not prove it is mirrormark, and the server, which cannot bind, exits with an error naming the ports. A port setting is Later. On a shared computer another user's program could do the same; other local users are out of scope (Section 11), and the effect would be no worse.
- **The traffic is not encrypted.** The challenge proves who is at each end; it does not hide the messages. Only a program that can capture loopback traffic, which on Linux needs root or `CAP_NET_RAW`, could read the bookmarks as they pass, and root can read the files anyway.
- **Changes from other profiles can wait up to a minute.** A background the browser stopped with no traffic starts again at the next periodic alarm. A change made in the browser itself wakes it at once.
- **Failed connection attempts show as errors.** When the first port is not mirrormark's, Chrome logs the failed socket in the extension's console, and may show it on the extensions page.
- **Upgrading the binary alone does not restart the server.** The running server keeps the old build until `install` runs, which restarts it every time, as the README's upgrade steps say.
- **Each `install --profile` restarts the server.** Every connected profile loses its connection and reconnects at its next alarm, within a minute; nothing is lost, since a round cut short writes no file.
- **The service runs only while the user is logged in**, which is when their browsers run. Without systemd, the user starts `mirrormark serve` themselves.

## Rollout

Chrome on Linux, end to end. Each phase is its own stack of commits, and nothing writes to a browser before Phase 4.

1. **Dev image, YAML database and merge engine.** `Dockerfile`, `bin/dev`, `Makefile`; the file formats, atomic writes, backups, config validation and `merge.Round`.
   - **Tests:** a table test per row and example in Section 4; property tests over random trees, three members and random round orders (some members closed, some rounds failing part-way) checking convergence, no writes after a quiet round, and no bookmark lost except by purge.
   - **Exit:** `bin/dev run make` passes.
2. **Extension and host.** The extension, the host, the bind page, and writing the database file. **Tests:** a Go fake extension drives the host. **Exit:** passes.
3. **`mirrormark install`.**
   - **Tests:** in the dev image, whose two system directories are writable so `install` needs no `sudo`, `install` runs against Google Chrome stable in two data directories driven over the DevTools pipe, with fresh profiles; it then runs again in a bound profile, which keeps its database and rebinds, and Chrome restarts with the binding kept. A second test lets `install` start Chrome itself for the bind.
   - **Exit:** the extension is enabled and bound, and reaches the host with no user action beyond the restart.
4. **Union sync.** Writes, `_deleted`, purges, the guard. **Tests:** e2e with two Chrome instances in one union covering each example in Section 4, a large folder delete that must not trip the guard, one browser closed while the other changes, and the latency target, measured with a handful of bookmarks. **Exit:** all pass, and a run on the developer's own desktop Chrome with test profiles and real `sudo` is recorded in the PR. That manual run is still to do; it is the one check left.

5. **Firefox Developer Edition** (Section 8). The extension runs in both browsers, the host accepts Firefox's arguments, `install --browser firefox` and `uninstall --browser firefox` work without root, and the dev image carries Firefox Developer Edition 159.0b2 beside Chrome. Chrome's unit and e2e tests pass after every commit.
   - **Tests:** unit tests for profile listing, the channel check, the files `install` writes, reading the UUID and binding; round tests with a Firefox-shaped tree and a union under `menu`. E2e, driven over WebDriver BiDi by a standard-library Go client: install and bind two Firefox profiles, bind a closed one, rebind, restart; edits, delete, restore and purge, a conflict, a first sync and a closed browser's catch-up, each between two Firefox profiles and between Firefox and Chrome; separators left alone and a purged folder that holds one; uninstall deactivating the add-on and restoring the prefs.
   - **Exit:** all pass, and the README's Firefox steps are run once by hand on a desktop with Developer Edition. That manual run is still to do; it is the one check left for this phase.

6. **Brave and LibreWolf** (Section 9). Refactors first, with Chrome and Firefox unchanged: each package describes its browsers, and install, bind and uninstall read from the description. Then a profile that has never run is skipped unless named (later replaced by Section 10's required `--profile`). Then Brave, install and uninstall in one commit with the shared-file rules, and LibreWolf likewise, so no prefix turns a profile's checks off without the uninstall that turns them back on. Then the dev image gains Brave 1.97.57 and LibreWolf 158.0-1, and the e2e tests run in them.
   - **Tests:** unit tests for each browser's data directory or profile root (with and without `$XDG_CONFIG_HOME`), the files `install` writes, recognising LibreWolf and refusing other Gecko browsers, skipping a never-run profile, and that uninstalling Brave keeps the shared CRX (and, before Phase 8, the host manifest) while Chrome is installed, and the other way round (and the same for the two Gecko browsers). E2e: install and bind a Chrome and a Brave profile with one id, then uninstall Brave and check that what Chrome still uses stays (run on Phase 8's transport); the same for a LibreWolf and a Developer Edition profile; and the five sync tests between Brave and LibreWolf.
   - **Exit:** all pass with Chrome's and Firefox's tests unchanged, and the README's Brave and LibreWolf steps are run once by hand on a desktop.

7. **Explicit profiles and binding once** (Section 10), landing before Phase 6's end-to-end tests, which then use it. First, `install` reads each named profile's state from its files, opens the bind page once only where the extension is ready, and says why elsewhere. Then `install` without `--profile` lists the profiles and changes nothing, and `--profile` takes an ID or a number.
   - **Tests:** unit tests that read each state in the profile-state spike from fixture files, for both families; that a profile that is not ready gets its message and no open; that each ready profile gets one open and a silent one ends in `not bound` after the wait; that a listing writes no file, starts no `sudo` and no browser, for Brave before and after an install and for Developer Edition with never-run and other-browser profiles; and the numeric-aware order and selection by ID and number (an exact ID beats a number, a number too large for any profile is refused as one). E2e: the existing Chrome and Firefox install tests with `--profile`, a listing of a bound Chrome profile, and a profile named before the browser installed the extension is reported not ready, then binds after a restart.
   - **Exit:** all pass, and the README's steps are run again on the desktop Brave that showed "blocked by Brave".

8. **A local server instead of native messaging** (Section 11). In order: the host reads and writes messages through a connection type, not stdin and stdout; a standard-library WebSocket server package; `mirrormark serve`, which checks `Host` and `Origin` and runs the challenge but which no extension uses yet; `install` writing `origin` and `token` and handing the token to the bind page; the systemd user service, installed and uninstalled together; the switch, in which the extension connects to the server and the e2e harness starts `mirrormark serve`; then removing native messaging. Phase 6's Brave and LibreWolf end-to-end tests follow, on the new transport: install and bind first, then the sync tests as a fourth pair, so the five sync tests of Phase 6 run for the other pairs as before. Every commit up to the switch leaves native messaging working, and every commit from the switch on uses the server, so no prefix leaves a user without a working transport.
   - **Tests:** as in Section 11, Tests.
   - **Exit:** all pass, the README's steps are run once on a desktop with Chrome and with Developer Edition, upgrading from the native-messaging build, and a server restart and a browser restart each reconnect. The e2e part is done, in all four browsers; the desktop run is still to do and is listed in `TASKS.md`.

**Rollback.** Remove a member from `config.yaml`, which stops its syncing without touching its bookmarks, or run `mirrormark uninstall`.

## Later

Each is its own RFC or stack once the Chrome version works.

- **Release Firefox and the rest of the family.** A signed XPI through `web-ext sign` [R13] for release Firefox, Nightly and ESR; reading the new profile switcher's names; tags and keywords. Open: whose addons.mozilla.org account signs.
- **Chromium, Brave Beta and Nightly.** Their profile directories and external extension directories; Chromium also honours `--load-extension` [R12].
- **`mirrormark export`** of a database or backup to a bookmarks HTML file both families import.
- **`mirrormark preview <union>`**, printing what a union would become before a member is added.
- **Firejail, Flatpak and Snap browsers.** The server runs outside the sandbox, so each needs only loopback access from inside it; none was tried.
- **A port setting**, for a computer where all three ports are taken.
- **Windows and macOS**, which refuse local CRX files [R11].
- **Release packaging**: `make release` and a README tested in a fresh container.
- **`mirrormark status`**, summarising profiles, unions, last rounds and errors; until then the log and the popup carry them.
- **A no-root install** for Chrome and Brave: Load unpacked in each profile.

## Success criteria

- Two Chrome profiles in one union match within 3 seconds of the last edit while both run (0.5 s quiet, up to 1 s polling, two rounds), and within 3 seconds of a closed one's extension connecting. Phase 4 measures this with a handful of bookmarks; a union of a few hundred is not measured yet.
- Each example in Section 4 gives the tree shown, in every member.
- In the property tests and the Phase 4 e2e, no bookmark goes missing except by purge, and a round with no user changes writes nothing.
- Two Firefox Developer Edition profiles, and a Firefox and a Chrome profile, in one union meet the same 3-second target, and each example in Section 4 gives the tree shown across the two browsers (Phase 5 e2e).
- A Brave and a LibreWolf profile in one union meet the same 3-second target, and each example in Section 4 gives the tree shown across the two (Phase 6 e2e).
- `mirrormark install` takes a fresh Chrome profile on Linux to a bound extension using only the steps it prints, and binds an already bound profile again to the same database (tested in the dev image). A used profile on the developer's desktop Chrome, with real `sudo`, is the manual check that remains.

## Security, privacy, operational impact

- **Security.** mirrormark targets a single-user desktop: programs running as the user are trusted, and other local users are out of scope (Section 11). The server listens on `127.0.0.1` only. It refuses an upgrade whose `Host` is not `127.0.0.1:<port>` or whose `Origin` is no database's, and runs no host until the connection proves, by a challenge, that it holds the named database's token; the server proves it holds the token first, and the token never crosses the socket (Section 11). The host validates every message and file and executes nothing it receives. The extension has no host permissions. The `sudo` step copies one JSON file into place with `install`, naming a CRX in the user's data directory; for Brave it writes Brave's file into `/opt/brave.com/brave/extensions/`. For Firefox and LibreWolf nothing runs as root, but each bound profile stops checking add-on signatures (Tradeoffs and risks).
- **Privacy.** The YAML files and backups hold the user's bookmarks in plain text, mode `0600`. Nothing leaves the computer, and there is no telemetry. `uninstall --purge` deletes a browser's databases and backups, and the last browser's purge deletes the rest.
- **Operations.** About 2 MB per database file per 10,000 bookmarks (estimate), times up to 30 backups. The server logs to `~/.local/state/mirrormark/log`; `install` prints its path. When the server starts and the log is over 1 MiB, it moves it to `log.1`. Every warning is logged each time; a failed round waits 5 seconds before the next, so a lasting problem adds a few lines every 5 seconds until it is fixed or the server restarts. The log does not rotate while the server runs, so such a problem grows one file for the whole session (a few MB a day); `TASKS.md` tracks rotating on write.

## Open questions

1. **One folder renamed or moved differently on two profiles.** _Resolved as built:_ theirs wins and the conflict is logged (Section 4). The alternative was one folder titled `X (a) / Y (b)`. Still awaiting the user's sign-off.
2. **Chrome account and local folders.** A signed-in Chrome profile can have two bookmark bars [R6]. _Resolved as built:_ when a path resolves, an account folder (`syncing`) wins over a local one of the same name.
3. **URL matching.** Should `http`/`https` or a trailing slash count as the same URL? _Resolved as built:_ no; URLs compare exactly.
4. **Release Firefox.** Whose addons.mozilla.org account signs the XPI, and is an unlisted signed add-on acceptable to the user? Until decided, only Developer Edition is supported.
5. **Should the server prove the token first?** _Resolved by the user's decision:_ yes. The first draft sent the token in the hello to whichever program answered first on the three ports. Now the extension and the server each prove they hold the token with an HMAC over the other's nonce, the server first, and the token never crosses the socket (Section 11). It costs one round trip per connection and Web Crypto in the extension.

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
- 2026-10-15 — trimmed for the minimal version: no `mirrormark status` or status files (install confirms a binding by the lock file's mtime; errors go to the log and the popup); `sudo install` replaces the `install-system-files` subcommand; no no-root fallback; `install` binds every profile unless `--profile` narrows it; `unpack` added.
- 2026-10-15 — after review of the merge engine: the wrong-tree guard counts purges and nodes missing from theirs; the extension stops a batch at the first failed op; `remove` never removes a folder's contents.
- 2026-10-15 — after review of the host and extension: a `status` message ends every round and carries its problems, and `error` is only for a host that stops; new codes `db-write-failed` and `db-lock-error`; failed rounds back off to 5 s and 30 s; the poll compares sizes too; the log rotates at 1 MiB and logs a repeated warning once; the extension reconnects on an alarm and waits a second after a new binding.
- 2026-10-15 — the extension sends `changed` at the first bookmark event of a burst, and the host's poll starts no round until the tree that follows, so no round runs on a tree older than the user's last edit.
- 2026-10-15 — final fixes: the wrong-tree guard counts a deleted folder once and catches an empty member folder and a wrong binding directly, so deleting a large folder no longer trips it; every warning is logged, without de-duplication; a failed round is retried after a fixed 5 s; `uninstall` keeps a host manifest naming another extension; a reconnect waits while a reconnect alarm is pending; open questions 1 to 3 recorded as built; Rollout and Success criteria say what was tested and that the manual run on desktop Chrome is still to do.
- 2026-10-15 — Firefox Developer Edition added as Section 8 and Phase 5 after a spike: one manifest for both browsers, per-profile install without root, the UUID read from `prefs.js` for binding, separators hidden from the host, `menu` for Firefox members only, release Firefox refused until signed.
- 2026-10-15 — Firefox plan trimmed: same-parent moves are not converted, since the merge never sends them; a Chrome member with a `menu` path is skipped like any missing top-level folder; Phase 5's e2e no longer probes URL schemes or XPI upgrades, which stay listed as risks.
- 2026-10-15 — Firefox install and uninstall as built: install turns uninstall's reset lines back in place and appends only missing prefs; writes keep a symlinked or private `user.js` as it is; databases are created before any profile is touched; uninstall writes `user.js` first, skips an unchanged one and says when it found no Firefox profile; `--purge` is per browser, and the shared files go with the last browser's databases.
- 2026-10-15 — after the final review of Phase 5: the dev image and e2e now cover Firefox over BiDi and D-Bus; the profiles root follows `$XDG_CONFIG_HOME`; the extension removes a purged folder's separators only when nothing else is inside; Phase 5's manual desktop run is recorded as still to do.
- 2026-10-15 — Brave and LibreWolf designed as Section 9 and Phase 6 after two spikes: each is a description in the existing Chrome or Firefox package, not a new install path; Brave shares Chrome's key, CRX and host manifest, and LibreWolf takes the Developer Edition route, recognised by `application.ini`; shared files go with the last browser that still has its own file in place; profiles that have never run are skipped unless named.
- 2026-10-15 — Brave and LibreWolf plan trimmed: `install` no longer refreshes the other Chromium browser's external file or writes only changed files; upgrades run `install` once per browser. The e2e checks that the shared host manifest stays after an uninstall instead of restarting the other browser.
- 2026-10-15 — after the user tried Brave on a desktop: `install` without `--profile` lists the profiles with their state and writes nothing; `--profile` takes an ID or a number, not a name; `install` reads each profile's extension state from its own files and opens the bind page once, only where the extension is ready, with no retries; the never-run skip goes. Both are the user's decisions (Section 10).
- 2026-10-15 — while implementing: a Chromium profile reads `removed` only while its `extensions.settings.<id>` has no `path`, so a copy loaded unpacked after a Remove is not reported as removed for ever.
- 2026-10-15 — profile listing as built: the example line gives the first unbound profile's quoted ID; a new `unreadable` state for a profile whose files cannot be parsed; `not <browser>` covers any refusal by the browser check, with the reason on stderr.
- 2026-10-15 — Section 11 and Phase 8 after the localhost transport spike, by the user's decision: one `mirrormark serve` per user, run as a systemd user service, in place of native messaging; the extension connects over a WebSocket to `127.0.0.1` on the first of three fixed ports that answers; the server checks `Host`, `Origin` and a per-profile token that `install` writes into the profile block and hands over through the bind page; protocol version 2 with a ping every 20 s; host manifests are no longer written, and old ones are removed.
- 2026-10-15 — by the user's decision (Open question 5, now resolved), the token no longer crosses the socket: the hello carries a nonce, the server answers a `challenge` with its own nonce and an HMAC proof bound to its port, and the extension checks it before sending its own `proof`. Hex encodings fixed; a port that fails the challenge is skipped; the e2e restart test adds a program on the first port that cannot prove itself. The risk of a program on the first port getting the token becomes one of a program holding all three ports stopping sync.
- 2026-10-15 — local server plan trimmed: the protocol version stays 1, since no peer of one transport meets the other; `install` restarts the service on every run instead of deciding when; the unit carries no `Environment=` lines; `install` leaves old host manifests for `uninstall`; the log still rotates only when the server starts, with rotating on write left in `TASKS.md`.
- 2026-10-16 — after review of the switch: until a server proves itself the popup shows only fixed texts naming the port, a known error outranks a wrong proof, a profile without a token is told to bind again, and the last purge keeps `serve.lock` while a server holds it.
- 2026-10-16 — native messaging removed as built: Sections 6 and 8 now say, as Section 11 does, that `install` leaves an old host manifest for `uninstall`; Section 11 adds that `uninstall` removes it even while another browser still uses mirrormark.
- 2026-10-16 — local server built: the e2e suite runs the real server in Chrome, Brave, Developer Edition and LibreWolf, with the sync tests as a fourth pair, Brave and LibreWolf, inside the 3 s target; Phase 8's desktop upgrade run, and Phase 6's, stay in `TASKS.md`.
- 2026-10-16 — after the final review, by the user's decisions: the threat model is a single-user desktop, with same-user programs trusted and other local users out of scope; before its proof a connection's failures are logged at Debug and summed up once a minute, never in its words, and an idle keep-alive connection is closed; `help` explains sharing folders and lists the files; `install` ends with a `config.yaml` to paste and says what it did with the service; the log records each `config.yaml` the server loads. The upgrade paragraph now says that an extension without a token dials nothing.
