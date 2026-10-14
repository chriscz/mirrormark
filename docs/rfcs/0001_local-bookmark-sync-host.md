# RFC 0001: Sync bookmark folders between browser profiles through YAML files

- Status: draft
- Author(s): Chris Coetzee, Claude
- Date: 2026-10-14 (Firefox Developer Edition added 2026-10-15; Brave and LibreWolf designed 2026-10-15)
- Links: none yet. Repository: `github.com/chriscz/mirrormark` (MPL-2.0).

## Problem

People who use more than one browser, such as Chrome for one profile and LibreWolf for another, have no local way to keep a bookmark folder the same in all of them. Each browser's own sync stays inside its family and goes through the vendor's servers. Tools that cross families, such as floccus [R10], need a server or cloud storage in the middle.

## Background

mirrormark is a WebExtension plus a small Go program. The user has fixed these choices: YAML files are the only database and config; conflicts are merged into suffixed copies such as `GitHub (chrome/personal)` and `GitHub (chrome/work)`, never resolved by picking a winner; deleting a bookmark inside a union moves it to a `_deleted` folder; Google Chrome on Linux comes first and the Firefox family follows; everything builds and tests in a Docker image.

This RFC describes the smallest version that works: two or more Chrome profiles on Linux (Phases 1 to 4), then Firefox Developer Edition profiles joining the same unions (Section 8, Phase 5), then Brave and LibreWolf as variants of those two installs (Section 9, Phase 6). What it leaves out is listed under [Later](#later).

**The bookmarks API.** Chrome exposes `bookmarks.getTree`, `get`, `create`, `update`, `move`, `remove` and change events [R5] [R6]. Node ids are stable but local to one profile, so mirrormark keeps its own ids. Chrome names its top-level folders by `folderType` (Chrome 134+). Firefox has the same API under `chrome.*`, with fixed ids for its top-level folders instead of `folderType`, and separators [R5].

**Native messaging** [R1] [R2]. The extension calls `runtime.connectNative(name)`, and the browser starts the program named in a host manifest file, after checking that the extension's id is listed in it. Messages are length-prefixed UTF-8 JSON, at most 1 MB towards the extension. The browser starts one process per connection and kills it when the connection closes. An open connection keeps a Chrome service worker alive [R3].

## Goals

1. **Unions stay the same.** After sync, every member folder of a union holds the same bookmarks and subfolders with the same titles and URLs.
2. **Several Chrome profiles on Linux**, and Firefox Developer Edition, Brave and LibreWolf profiles in the same unions, in a design other browsers of both families can join later.
3. **Merge, never drop.** When two profiles change the same thing differently, both versions survive as suffixed copies.
4. **Nothing is lost.** Sync removes a bookmark only after the user removes it from `_deleted`.
5. **Near-immediate.** A change in one running profile shows in another running profile within a few seconds.
6. **Simple to run and install.** One Go binary that embeds the extension. No daemon, no network listener, no account. The YAML files are the whole state, and a person can read them.

## Non-goals

- **Asking which side wins.** The engine reports each conflict so a later mode can ask.
- **Syncing reorders.** A new bookmark takes its position from its source; moving a bookmark within its folder stays local.
- **Editing the bookmark lists in database files by hand** (Section 3).
- **Syncing between computers**, or tabs, history and settings.
- **Store listings** on the Chrome Web Store or addons.mozilla.org, and release Firefox, which needs a signed add-on (Later).

## Proposal

We propose plain native messaging, one host process per browser profile, and YAML files as the only shared state. The host processes never talk to each other; they take turns on the files under one lock.

```
 Chrome (personal)       Chrome (work)          mirrormark CLI
   extension               extension            (install, uninstall, unpack)
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

It runs a **round** on connect, whenever the extension reports a change, and whenever `config.yaml` or another member's database file changes. It notices file changes by comparing modification times and sizes once a second (`os.Stat`). A round:

1. takes the global lock (`flock` on `~/.local/share/mirrormark/lock`);
2. reads `config.yaml` and the database file of every profile that shares a union with this one;
3. merges each union (Section 4) and sends this browser the writes it needs;
4. if every write succeeded and the bookmarks changed, writes this profile's database file, setting the `seq` of each union record it changed to one above the highest `seq` of that union it read;
5. releases the lock.

A host writes only its own browser and its own file; other profiles catch up in their next round. If any write fails, or the browser does not answer for 10 seconds, the round writes no file, logs a warning, and the next round starts again from the same file (Section 4, Why it converges). A failed round is retried 5 seconds later, or sooner when a file changes, until a round succeeds, so a browser that keeps failing does not take the global lock, or add to the log, every second. If the browser made every change but the host cannot write its file, the round reports `db-write-failed` and is retried the same way. A crashed host releases its locks with it.

If another member's file is missing or unreadable, the round skips every union that member belongs to, logs a warning and sends it to the popup. It never treats a member it cannot read as empty.

### 2. The protocol

```jsonc
// extension → host, first message; "db" is set by `mirrormark install` (Section 5)
{"v": 1, "type": "hello", "db": "chrome-personal", "browser": "chrome/141.0"}   // or "firefox/159.0"

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

// either direction; from the host, only when it stops
{"v": 1, "type": "error", "code": "db-locked", "message": "…"}
```

- **Whole trees.** The extension always sends its whole tree, and the host compares it with the database file, so a missed event is fixed by the next tree.
- **No rounds on an old tree.** While the extension waits for quiet, the host's copy of the tree lacks the user's latest edits, and a round that a file change starts then could create again what the user just made, such as the member folder. So the extension sends `changed` at once, and the host's poll waits for the tree that follows.
- **No blind writes.** `update`, `move` and `remove` carry what the host expects to find. The extension reads the node first and fails the op as `stale` if it differs, so an edit the user makes during a batch survives and the next round sees it as a change. Ops run one at a time, as MDN advises when order matters [R5], and the extension stops at the first op that fails and answers the rest `not run`: each op assumes the ones before it landed. A `remove` takes one bookmark or one empty folder (`bookmarks.remove`, never `removeTree`), and the host removes a folder's contents before the folder, so a folder that still holds something fails to go instead of taking it along.
- **Problems.** The popup shows the last `status` message's problems, so a skipped union or an invalid config stays visible until a round no longer finds it. The host's `error` codes are `unsupported-version`, `bad-db`, `db-missing`, `db-locked` and `db-lock-error` (the lock file cannot be locked at all); the `status` codes are `config-invalid`, `db-missing`, `union-skipped`, `db-mismatch`, `apply-failed`, `db-write-failed` and `db-lock-error`.
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

Neither the extension nor the host can learn which browser profile the extension runs in [R1] [R2], and with the CRX route (Section 6) the same extension runs in every profile. So `mirrormark install` binds it: after creating the profile's database file, it runs

```
google-chrome --profile-directory="Profile 1" "chrome-extension://<id>/bind.html#chrome-personal"
```

which opens a tab in that profile (in the running Chrome if there is one). The page stores the database id in `storage.local`, says so, and closes. The page is not web-accessible, so a web page cannot open it. The extension sends the id in every `hello`; an unbound extension stays idle and its popup says to run `mirrormark install`. If the database file is gone or another host holds its lock (a copied profile), the host answers `error` and the popup says why. Reinstalling the extension clears `storage.local`; running `mirrormark install` again rebinds it.

This works in Google Chrome 155 on Linux, with Chrome running or not, once the extension is loaded in that profile (checked in Docker while planning). The first time a profile opens after the extension was installed, the tab can load before the extension does and shows an error. So `install` confirms each binding: the host touches `profiles/<id>.lock` on each `hello`, `install` waits up to 4 seconds for its modification time to move, and opens the page again, up to 3 times.

### 6. Installing

The binary embeds the extension (`go:embed`). `mirrormark install` (for Chrome, the default `--browser chrome`; Firefox is Section 8):

1. lists Chrome's profiles from `~/.config/google-chrome/Local State` (`profile.info_cache`; `--user-data-dir` picks another data directory);
2. binds every listed profile, or only those named with `--profile "Profile 1"` (a directory, or a name only one profile has; repeatable). Binding a profile only creates its database file; nothing syncs until `config.yaml` names it;
3. unpacks the extension into `~/.local/share/mirrormark/extension/` and packs `mirrormark.crx` beside it;
4. writes two system files, as root: `/usr/share/google-chrome/extensions/<id>.json` naming the CRX and its exact version, and the native host manifest `/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json`, which reaches every Chrome data directory. Branded Chrome ignores `--load-extension` since 137 [R12], but on Linux it installs a CRX named this way without a prompt, in every profile [R11] (both directories checked in the spike). If it cannot write them itself, it copies them into place with `sudo install -D -m 0644`, so the root step runs no mirrormark code;
5. for each profile it binds, creates the database file (id `chrome-<profile name>`, label `chrome/<profile name>`, made unique if taken), and `config.yaml` if it is missing;
6. if step 4 changed either system file, asks the user to restart Chrome and run `mirrormark install` again, because Chrome reads the extensions directory only at startup; otherwise it binds each one (Section 5).

Without `sudo`, `install` stops and says so; a no-root route is listed under [Later](#later). `mirrormark unpack [DIR]` does step 3 alone, into `DIR` if given, and prints the folder, for Load unpacked while developing or in a profile where the user removed the extension (Chrome does not reinstall it there). The unpacked manifest carries the install's `key`, so the id is the same as the installed one.

**A key per install.** Chrome derives an extension's id from its public key. `install` generates a key pair with Go's standard crypto, writes the public key into the unpacked extension's manifest `key`, signs the CRX with the private key, puts the id in the host manifest's `allowed_origins`, and keeps the private key in `~/.local/share/mirrormark/extension.pem` (mode `0600`) so upgrades keep the id. No private key is shipped, so an extension built elsewhere does not get this install's id. An upgrade re-signs with the same key and raises `external_version` so Chrome picks up the new build.

`mirrormark uninstall` removes the host manifest, the preferences file, the unpacked extension and the CRX, and keeps the YAML files and the key unless given `--purge`. `--purge` deletes Chrome's databases, their backups and the key, and the config, log and data directory only if no Firefox profile's database is left, since that browser's profiles still sync through them and its `uninstall` finds its profiles through their databases. A host manifest that names another extension belongs to another install, so `uninstall` leaves it in place. Removing the preferences file removes the extension from every Chrome profile at Chrome's next start.

### 7. Extension and code

The extension is thin: Manifest V3, permissions `alarms`, `bookmarks`, `nativeMessaging` and `storage`, plain JavaScript with no build step, so the binary embeds the source files as they are. It connects from top-level service-worker code, registers `runtime.onStartup` so Chrome starts the worker when a profile opens, and reconnects on each bookmark event. After a failed connection it waits instead for an alarm (30 s, or 60 s after the host's `error`), because the worker's timers stop with it, and while that alarm is pending neither a bookmark event nor a restarted worker reconnects; after a new binding it waits a second so the old host can release the database. It sends `changed` at the first bookmark event of a burst and the tree after it, applies ops, and shows in its popup the bound profile, why the host stopped if it did, and the last round's problems. Besides `install` and `uninstall`, the CLI has `unpack` (above) and `host` (what the native host manifest runs; Chrome starts the binary with the extension's origin as its only argument, which also selects host mode).

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
- it reads a disconnect's reason from the port's `error` as well as `runtime.lastError`.

An open native port keeps a Firefox event page alive as it keeps a Chrome service worker alive (160 s checked).

**Separators.** Firefox has separators (`type: "separator"`); Chrome has none. The extension never sends them to the host, and a node's `index` in the tree counts only the siblings that are not separators. When it applies a `create` or `move` with an index, it maps that index to the real one: the position of the index-th sibling that is not a separator, or the end. Order does not sync (Non-goals), so a separator can end up between different neighbours than before; mirrormark never moves, edits or copies one. The one exception: Firefox, like Chrome, refuses to remove a folder that is not empty, and the host removes a folder only once everything it knows inside is gone, so before a `remove` of a folder that holds only separators, the extension removes them. This happens only in a purge, and a separator holds nothing. A folder that holds anything else keeps its separators, and its `remove` fails as it would without them.

**Same-parent moves.** Firefox reads a move's index within the same parent as the final position; Chrome counts it before the node leaves its old place. The merge never sends such a move (reordering stays local), so the extension does not convert between the two; a merge that starts sending them must add the conversion.

**The `menu` folder.** Paths may start with `menu` (or `Bookmarks Menu`), but only for Firefox members. A Chrome member whose path starts with `menu` has its union skipped (`union-skipped`, no top-level folder `menu`), as for any top-level folder a browser lacks; the other members go on. Firefox has no account folders, so `syncing` never applies.

**Ids and labels.** A Firefox profile's database id is `firefox-<slug of its name>` and its label `firefox/<slug>`; `browser` in its profile block is `firefox`. Firefox's node ids are 12-character GUIDs rather than small integers.

**The host.** Firefox starts the host with two arguments, the host manifest's path and the extension's id, instead of Chrome's one origin. `mirrormark` runs as the host when its first argument ends in `com.github.chriscz.mirrormark.json`. Nothing else in the host changes; framing and the 1 MB limit towards the extension are the same [R1].

**Installing**, without root. `mirrormark install --browser firefox [--user-data-dir ROOT] [--profile NAME]...`:

1. lists the profiles: `ROOT` is `--user-data-dir`, else `$XDG_CONFIG_HOME/mozilla/firefox` (`~/.config/mozilla/firefox` when that is unset) if it holds `profiles.ini` (Firefox 147 and later [R15]), else `~/.mozilla/firefox`. It reads `profiles.ini` and adds every `ROOT/*/` that holds a `prefs.js` and is not listed, named by its directory after the first `.`; Firefox's new profile switcher records its profiles only in an SQLite file, which the standard library cannot read;
2. picks every profile or those named with `--profile` (a directory or a name), and refuses any that is not Developer Edition, as above, before writing anything;
3. creates each profile's database file, and `config.yaml` if missing, as for Chrome, before it writes into any profile, so a write that fails part way never leaves a profile with its checks off and no database for `uninstall` to find it by;
4. writes, as the user: the native host manifest `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json` with `allowed_extensions` naming the gecko id (Firefox does not read a host manifest under `~/.config/mozilla/`); and in each picked profile `extensions/mirrormark@chriscz.github.io.xpi`, packed from the embedded files like the CRX, and three lines in `user.js`: `xpinstall.signatures.required` false, `extensions.autoDisableScopes` 14 and `extensions.startupScanScopes` 1. A line `uninstall` reset is turned back where it is, and only prefs still missing are appended, so install and uninstall cycles add no lines. A `user.js` that is a symlink is written where it points, and an existing one keeps its mode, so a file kept with the user's dotfiles or made private stays so. On its next start Firefox installs the add-on enabled with no prompt; 14 clears only the profile bit of Firefox's default 15, so add-ons sideloaded into the user, application or system scopes still start disabled and ask;
5. if it changed any of those files, asks the user to restart Firefox in those profiles if it is running and to run `install` again; otherwise it binds each profile.

**Binding.** The extension's pages are at `moz-extension://<uuid>/`, with a UUID Firefox picks per profile and keeps in `prefs.js` (`extensions.webextensions.uuids`). Pinning it in `user.js` would replace every add-on's UUID and lose their storage, so `install` reads it. If it is missing, `install` starts that profile's Firefox and waits up to 15 seconds for it to appear. It then runs `<LastPlatformDir>/firefox --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>`, which opens the page in that profile whether Firefox runs there or not (a running one gets it over D-Bus), and confirms the binding through the lock file's mtime with up to 3 tries, as for Chrome (Section 5). `about:blank` is there because `bind.html` closes its tab, and closing the only tab of the only window quits Firefox.

**Uninstalling.** `mirrormark uninstall --browser firefox` removes the XPI from each profile whose database file says `browser: firefox`, sets `xpinstall.signatures.required` back to true and `extensions.autoDisableScopes` back to 15 (their defaults) in its `user.js`, and removes the per-user host manifest if it names the gecko id. It leaves `extensions.startupScanScopes` at 1, which only makes Firefox look in the profile's own `extensions` folder at start, so that it notices the XPI is gone. The reset applies to every line that sets those prefs to mirrormark's values, including lines the user wrote, which only turns the checks back on. It writes the `user.js` files before it removes anything, so if a write fails, running `uninstall` again finishes the job, and it leaves a `user.js` with nothing to change untouched. If no database names a Firefox profile, it changes no `user.js` and says so, naming the two prefs to check by hand. It keeps the YAML files unless given `--purge`, which deletes the Firefox profiles' databases and their backups, and the config, log and data directory only if no Chrome profile's database is left.

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

Everything else is shared within a family: Chrome and Brave share the key, the CRX, the extension id and the system host manifest; Developer Edition and LibreWolf share the XPI, the three `user.js` lines and their resets, the per-user host manifest, the UUID lookup and the bind URL.

**Brave.** Brave reads external extension files from its executable's directory, `/opt/brave.com/brave/extensions/`, and native host manifests only from Chrome's `/etc/opt/chrome/native-messaging-hosts/`, whether or not Chrome is installed. The extension id depends only on the key, so one host manifest with one `allowed_origins` entry serves both browsers. Brave installs the CRX enabled and without a prompt, in existing profiles and in profiles created later. Its `Local State` has the same `profile.info_cache` as Chrome's, and binding runs `brave-browser` with Chrome's arguments and Chrome's three tries: the first open in a profile after the install loses the same race to the extension. Brave's user agent is Chrome's, so the hello says `chrome/155.0.0.0`; the host does not need to tell the two apart, because the database id carries the profile's identity. `/usr/share/chromium/extensions/` also works in Brave but reaches Chromium too, so we use Brave's own directory.

**Shared Chromium files.** `install --browser brave` packs the same CRX and writes Brave's external file and the host manifest, with `sudo` as for Chrome. Every external file names that one CRX and its exact version, so a Chrome file left at the old version after an upgrade would stop Chrome installing the new build. So `install` for either browser also rewrites the other's external file if it is present, writes only the files that changed, asks the user to restart the browser it installs into, and says that the other picks up the new build at its next start. `uninstall --browser brave` removes Brave's external file. It removes the CRX, the unpacked extension and the host manifest only if no other Chromium browser's external file is still in place, and otherwise says which browser still uses them. `--purge` deletes Brave's databases and backups, and the key only if no other Chromium browser has its external file in place or a database left; the same rules apply to Chrome with Brave as the other browser.

**What a Brave or Chrome user sees.** Both browsers show a "Safety Check" card on their extensions page saying they cannot verify where mirrormark comes from (branded Chrome 155 shows it for the same install); the extension stays on. Brave restores the last session at start, where Chrome opens a new tab, so a `bind.html` tab left from a failed try opens again at the next start and binds the profile again. Every try of one `install` opens the same database id, so this rebinds to the same database.

**LibreWolf.** LibreWolf is a release build, but it is built without `MOZ_REQUIRE_SIGNING` and honours `xpinstall.signatures.required`, which settles the conflicting reports [R14] for 158.0-1; none of the three prefs is locked, and their defaults are Firefox's (`true`, 15), so uninstall's resets are right. The Developer Edition route works unchanged: the XPI in the profile's `extensions` folder, the three `user.js` lines, the host manifest in `~/.mozilla/native-messaging-hosts/` (LibreWolf reads it after `~/.librewolf/native-messaging-hosts/` and `/usr/lib/librewolf/native-messaging-hosts/`, so a manifest there takes precedence [R8]), the UUID from `prefs.js`, and `<bin> --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>`, cold or into a running LibreWolf over D-Bus. Its `channel-prefs.js` says `release`, so the channel cannot tell it from release Firefox; `application.ini`'s `Name=LibreWolf` can. Its user agent stays `Firefox/158.0` under `resistFingerprinting`, so its hello says `firefox/158.0`. LibreWolf clears cookies and cache at shutdown by default, but the binding in `storage.local` survived clean restarts.

**Which Gecko profiles install accepts.** The Developer Edition-only gate becomes "the browser asked for": `--browser firefox` takes only profiles whose browser is on the `aurora` channel, `--browser librewolf` only profiles whose browser's `application.ini` says `Name=LibreWolf`. Any other Gecko browser, release Firefox among them, is still refused before anything is written, and the message names the two that work.

**Shared Gecko files.** Developer Edition and LibreWolf read one host manifest. `uninstall --browser librewolf` removes the XPI and resets the prefs in LibreWolf's profiles, and removes the host manifest only if no Developer Edition profile named by a database still holds the XPI; the same applies the other way round. As for Chromium, it is the files still in place that decide, not the databases, which an uninstall without `--purge` leaves behind.

**Profiles that have never run.** A first LibreWolf start creates two profiles, and the second, `default`, never runs, so it has no `compatibility.ini` and `install` cannot tell which browser it belongs to. When the user named no `--profile`, `install` now skips such a profile with one line (`skipped: default: it has never run; …`) and goes on with the rest; when the user named it, `install` refuses as before. If every profile is skipped, `install` stops. The same code serves Developer Edition, so this applies there too.

**Ids and labels.** A Brave profile's database id is `brave-<slug>` and its label `brave/<slug>`, so a conflict copy reads `GitHub (brave/personal)`; a LibreWolf profile's are `librewolf-<slug>` and `librewolf/<slug>`. `browser` in the profile block is `brave` or `librewolf`. `mirrormark install` and `uninstall` accept `--browser chrome`, `brave`, `firefox` or `librewolf`, and `--purge` works per browser as before.

**Not supported.** Only the packagings the spikes ran are supported: Brave stable from Brave's `.deb` (`/opt/brave.com/brave`) and LibreWolf from its official tarball. Brave Beta and Nightly have other binaries and data directories; Flatpak and Snap browsers restrict native messaging (Later). LibreWolf from its Debian/Ubuntu repository, AppImage or AUR runs from another directory; `install` may well work with it, since it reads the directory from `compatibility.ini`, but nobody has tried. The README lists these as untested.

**Decisions taken for the user.** The user was away when this section was written; each of these is the smallest safe choice and awaits sign-off:

- Brave and LibreWolf are descriptions in the existing Chrome and Firefox packages, not new install paths;
- Brave shares Chrome's key, CRX, id and system host manifest, and writes its external file to `/opt/brave.com/brave/extensions/`;
- shared files go with the last browser that still has its own file in place (the external file for Chromium, the XPI for Gecko); the Chromium key also stays while another Chromium browser's databases remain;
- `install` for one Chromium browser refreshes the other's external file, so an upgrade does not leave it naming an old version;
- databases are `browser: brave` / `librewolf`, ids `brave-<slug>` / `librewolf-<slug>`, labels `brave/<slug>` / `librewolf/<slug>`; the host does not tell browsers apart from the hello;
- LibreWolf takes the Developer Edition route unchanged, recognised by `application.ini`, and every other Gecko browser is still refused before anything is written;
- profiles that have never run are skipped with a note unless named, in Developer Edition too;
- the e2e harness keeps `remote.prefs.recommended` false: the remote agent's recommended prefs set `extensions.autoDisableScopes` to 0, which would hide whether install's prefs work.

## Alternatives considered

**Per-node versions** (the previous draft). Each node carried a version `v` and the profile `by` that made it, and a `purged` list with 90-day expiry recorded real deletions. A three-way merge against each profile's own last file gives the same answers with less state: base tells a removal from a node a profile never had, so no purge list is needed and a long-closed profile no longer brings purged bookmarks back; per-field merging replaces whole-node versions, so a title edit and a URL edit no longer conflict. The global lock already gives the total order the versions stood in for, and `seq` records it.

**Telling hosts about changes** through inotify or by waking the other extension. Polling `os.Stat` once a second on a few files costs nothing measurable, needs no dependency, and works the same on every platform. A host cannot wake another profile's extension.

**A checksum over each database file.** It caught hand edits so they were not misread as browser changes. A hand edit can at worst move bookmarks to `_deleted`, the wrong-tree guard catches a truncated file, and the YAML parser catches a corrupt one, so the checksum is dropped.

**Choosing the profile in the popup** (the previous draft). On first connect the host offered the unbound databases and the user picked one. Binding at install time removes the `choose` message, the `claimed` flag and the unclaim step, and the user cannot pick the wrong profile. The spike showed that Chrome opens the bind page from the command line, so the popup pick is not needed.

**TypeScript and Puppeteer.** A `tsc` build and a Node test driver would bring Node and npm into the dev image for a few hundred lines of extension code. Plain JavaScript and a Go DevTools client over a pipe keep one toolchain.

**One daemon behind a native-messaging relay.** Each host process relayed to a long-running `mirrormark serve` over a Unix socket. It needed a daemon that outlives the browser's host and both browsers open at once. With the files standing in for the other members, a round needs only its own browser, and a closed browser catches up when it starts.

**A localhost WebSocket server.** Any local program or web page can reach a localhost port, so it needs tokens, `Origin` checks and DNS-rebinding defences. Native messaging lets the browser vouch for the extension.

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
- **`install` opens a window in every profile it binds.** With many profiles, `--profile` limits it.
- **Installation depends on undocumented files** (`Local State`, the preferences-file directory) that Chrome may change.
- **The guard can refuse real changes.** Removing more than half of a union one node at a time, or emptying the member folder, trips it; deleting one folder that holds as much, or moving the bookmarks into `_deleted`, does not. Emptying a `_deleted` that holds more than half of the union node by node trips it too; removing the `_deleted` folder itself does not. A profile that was closed while another one moved most of the union to `_deleted` and purged it sees the guard every round, and must move its database file aside and re-run `install` to become a new member.
- **No export yet.** Recovering from a bad sync means `_deleted`, the YAML backups read by hand, or the browser's own bookmark export made beforehand; `install` suggests making one.
- **Unsigned add-ons in Firefox profiles.** `install` sets `xpinstall.signatures.required` to false in each Firefox profile it binds, so that profile accepts any unsigned add-on, not only mirrormark's, and with `extensions.autoDisableScopes` 14 an add-on file dropped into its `extensions` folder starts enabled without asking. Add-ons sideloaded into the other scopes, such as `~/.mozilla/extensions`, still start disabled and ask, as by default. A program that can write the profile directory could already change the profile in other ways, but the user loses Mozilla's signature check there. The README says so before the user runs `install`; `uninstall` restores both prefs.
- **Only Developer Edition.** Release Firefox, which most people use, needs a signed XPI. Until then Firefox support reaches only people who run Developer Edition.
- **Firefox upgrades are unchecked.** An upgrade replaces the XPI in each profile; that Firefox picks up a changed XPI at its next start (with `startupScanScopes` 1) was not checked in the spike, and Phase 5's e2e does not check it either. If Firefox kept the old build it would go on running it, and the host refuses an old build only when the protocol version changed (`unsupported-version` in the log).
- **Undocumented Firefox files.** `profiles.ini`, `compatibility.ini`, `channel-prefs.js` and the UUID map in `prefs.js` are not documented interfaces. The new profile switcher's profiles are found by scanning for `prefs.js`, and get the directory's name rather than the one Firefox shows.
- **URLs Firefox refuses.** A Chrome bookmark whose URL Firefox will not store (perhaps `chrome://` or `file:` URLs; not checked) fails its `create` in Firefox every round, which stalls that Firefox profile like any failing write. Phase 5 does not check which schemes Firefox refuses; the stalled profile logs the failing op.
- **Brave's file lives in Brave's package tree.** Whether a Brave upgrade or removal leaves `/opt/brave.com/brave/extensions/` alone was not checked. If an upgrade removed the file, Brave would remove mirrormark at its next start, and running `install --browser brave` again would put it back.
- **Brave's own features were not tried with mirrormark:** Brave Sync (whether it changes `syncing` or the bookmark ids), the policy route, and CDP with the default data directory.
- **A restored bind tab from an older install.** If the user moved a Brave profile's database aside and installed again, a failed tab from before could open at the next start and bind the old id; the popup then reports `db-missing`, and running `install` again fixes it.
- **LibreWolf's future and other packagings.** A later LibreWolf could turn on `MOZ_REQUIRE_SIGNING` or lock one of the three prefs; `install` would still write them, and the add-on would not load (`not bound`). Only the tarball in `/opt/librewolf` and LibreWolf's default sanitize settings were run; a user who makes LibreWolf clear more at shutdown may lose the binding at each restart, which the popup reports as unbound.
- **A host manifest in `~/.librewolf/native-messaging-hosts/`** wins over mirrormark's in `~/.mozilla/`. A user who put one there for mirrormark by hand would have to keep it current.
- **Same-user programs are trusted.** They can rewrite the YAML files, as they could the browsers' own files.

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

6. **Brave and LibreWolf** (Section 9). Refactors first, with Chrome and Firefox unchanged: each package describes its browsers, and install, bind and uninstall read from the description. Then a profile that has never run is skipped unless named. Then Brave, install and uninstall in one commit with the shared-file rules, and LibreWolf likewise, so no prefix turns a profile's checks off without the uninstall that turns them back on. Then the dev image gains Brave 1.97.57 and LibreWolf 158.0-1, and the e2e tests run in them.
   - **Tests:** unit tests for each browser's data directory or profile root (with and without `$XDG_CONFIG_HOME`), the files `install` writes, recognising LibreWolf and refusing other Gecko browsers, skipping a never-run profile, and that uninstalling Brave keeps the shared host manifest and CRX while Chrome is installed, and the other way round (and the same for the two Gecko browsers). E2e: install and bind a Chrome and a Brave profile with one id, then uninstall Brave and check Chrome still reaches its host; the same for a LibreWolf and a Developer Edition profile; and the five sync tests between Brave and LibreWolf.
   - **Exit:** all pass with Chrome's and Firefox's tests unchanged, and the README's Brave and LibreWolf steps are run once by hand on a desktop.

**Rollback.** Remove a member from `config.yaml`, which stops its syncing without touching its bookmarks, or run `mirrormark uninstall`.

## Later

Each is its own RFC or stack once the Chrome version works.

- **Release Firefox and the rest of the family.** A signed XPI through `web-ext sign` [R13] for release Firefox, Nightly and ESR; reading the new profile switcher's names; tags and keywords. Open: whose addons.mozilla.org account signs.
- **Chromium, Brave Beta and Nightly.** Their profile directories and external extension directories; Chromium also honours `--load-extension` [R12].
- **`mirrormark export`** of a database or backup to a bookmarks HTML file both families import.
- **`mirrormark preview <union>`**, printing what a union would become before a member is added.
- **Flatpak and Snap browsers**, which restrict native messaging.
- **Windows and macOS**, which refuse local CRX files [R11].
- **Release packaging**: `make release` and a README tested in a fresh container.
- **`mirrormark status`**, summarising profiles, unions, last rounds and errors; until then the log and the popup carry them.
- **A no-root install**: a per-user host manifest in `<data dir>/NativeMessagingHosts/` plus Load unpacked in each profile.

## Success criteria

- Two Chrome profiles in one union match within 3 seconds of the last edit while both run (0.5 s quiet, up to 1 s polling, two rounds), and within 3 seconds of a closed one's extension connecting. Phase 4 measures this with a handful of bookmarks; a union of a few hundred is not measured yet.
- Each example in Section 4 gives the tree shown, in every member.
- In the property tests and the Phase 4 e2e, no bookmark goes missing except by purge, and a round with no user changes writes nothing.
- Two Firefox Developer Edition profiles, and a Firefox and a Chrome profile, in one union meet the same 3-second target, and each example in Section 4 gives the tree shown across the two browsers (Phase 5 e2e).
- A Brave and a LibreWolf profile in one union meet the same 3-second target, and each example in Section 4 gives the tree shown across the two (Phase 6 e2e).
- `mirrormark install` takes a fresh Chrome profile on Linux to a bound extension using only the steps it prints, and binds an already bound profile again to the same database (tested in the dev image). A used profile on the developer's desktop Chrome, with real `sudo`, is the manual check that remains.

## Security, privacy, operational impact

- **Security.** No network listener; only the extension id in the host manifest can start the host [R1] [R2]. The host validates every message and file and executes nothing it receives. The extension has no host permissions. The `sudo` step copies two JSON files into place with `install`: one names a CRX in the user's data directory, the other the host binary. For Brave the same `sudo install` writes Brave's file into `/opt/brave.com/brave/extensions/`. For Firefox and LibreWolf nothing runs as root, but each bound profile stops checking add-on signatures (Tradeoffs and risks); only the gecko id in the per-user host manifest can start the host.
- **Privacy.** The YAML files and backups hold the user's bookmarks in plain text, mode `0600`. Nothing leaves the computer, and there is no telemetry. `uninstall --purge` deletes a browser's databases and backups, and the last browser's purge deletes the rest.
- **Operations.** About 2 MB per database file per 10,000 bookmarks (estimate), times up to 30 backups. The host logs to `~/.local/state/mirrormark/log`; `install` prints its path. A host that starts with the log over 1 MiB moves it to `log.1`. Every warning is logged each time; a failed round waits 5 seconds before the next, so a lasting problem adds a few lines every 5 seconds until it is fixed or the host restarts.

## Open questions

1. **One folder renamed or moved differently on two profiles.** _Resolved as built:_ theirs wins and the conflict is logged (Section 4). The alternative was one folder titled `X (a) / Y (b)`. Still awaiting the user's sign-off.
2. **Chrome account and local folders.** A signed-in Chrome profile can have two bookmark bars [R6]. _Resolved as built:_ when a path resolves, an account folder (`syncing`) wins over a local one of the same name.
3. **URL matching.** Should `http`/`https` or a trailing slash count as the same URL? _Resolved as built:_ no; URLs compare exactly.
4. **Release Firefox.** Whose addons.mozilla.org account signs the XPI, and is an unlisted signed add-on acceptable to the user? Until decided, only Developer Edition is supported.

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
