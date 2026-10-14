# RFC 0001: Sync bookmark folders between browser profiles through YAML files

- Status: draft
- Author(s): Chris Coetzee, Claude
- Date: 2026-10-14
- Links: none yet. Repository: `github.com/chriscz/mirrormark` (MPL-2.0).

## Problem

People who use more than one browser, such as Chrome for one profile and LibreWolf for another, have no local way to keep a bookmark folder the same in all of them. Each browser's own sync stays inside its family and goes through the vendor's servers. Tools that cross families, such as floccus [R10], need a server or cloud storage in the middle.

## Background

mirrormark is a WebExtension plus a small Go program. The user has fixed these choices: YAML files are the only database and config; conflicts are merged into suffixed copies such as `GitHub (chrome/personal)` and `GitHub (librewolf)`, never resolved by picking a winner; deleting a bookmark inside a union moves it to a `_deleted` folder; Google Chrome on Linux comes first and the Firefox family follows; everything builds and tests in a Docker image.

**One bookmarks API.** Both families expose `bookmarks.getTree`, `get`, `create`, `update`, `move`, `remove` and change events [R5] [R6]. Node ids are stable but local to one profile, so mirrormark keeps its own ids. Chromium names its top-level folders by `folderType` (Chrome 134+); Firefox uses fixed ids such as `toolbar_____` (unverified against documentation). Only Firefox has separators.

**Native messaging** [R1] [R2]. The extension calls `runtime.connectNative(name)`, and the browser starts the program named in a host manifest file, after checking that the extension's id is listed in it. Messages are length-prefixed UTF-8 JSON, at most 1 MB towards the extension. The browser starts one process per connection and kills it when the connection closes. An open connection keeps a Chromium service worker [R3] and a Firefox event page [R4] alive.

## Goals

1. **Unions stay the same.** After sync, every member folder of a union holds the same bookmarks and subfolders with the same titles and URLs.
2. **Any mix of browsers**, including several profiles of one browser, starting with Google Chrome on Linux.
3. **Merge, never drop.** Both conflicting versions survive, as suffixed copies.
4. **Nothing is lost.** Sync removes a bookmark only after the user purges it from `_deleted`.
5. **Simple to run.** No daemon, no network listener, no account. The YAML files are the whole state, and a person can read them.
6. **Simple to install.** One command lists the browsers' profiles, lets the user pick which ones sync, and installs the extension and native host as far as each browser allows.

## Non-goals

- **Asking which side wins.** The engine reports each conflict so a later mode can ask.
- **Syncing reorders.** A new bookmark takes its position from its source; moving a bookmark within its folder stays local.
- **Editing the bookmark lists in database files by hand** (Section 3).
- **Syncing between computers**, or tabs, history and settings.
- **Tags, keywords, descriptions and separators**, which only Firefox has.
- **Store listings** on the Chrome Web Store or addons.mozilla.org.
- **Flatpak and Snap browsers**, which restrict native messaging, until someone tests them.
- **Windows and macOS** until Phase 6.

## Proposal

We propose plain native messaging, one host process per browser, and YAML files as the only shared state. The host processes never talk to each other; they take turns on the files under one lock.

```
 Chrome (personal)       LibreWolf              mirrormark CLI
   extension               extension            (install, status, preview, export)
      │ native messaging      │ native messaging        │
 mirrormark host         mirrormark host                │
      └───────── lock, read, write ───┬─────────────────┘
   ~/.config/mirrormark/config.yaml   unions; written by the user
   ~/.local/share/mirrormark/
     profiles/<id>.yaml               one database per profile
     backups/<id>/<time>.yaml
     lock
```

### 1. The host: one process per browser, no daemon

The browser starts `mirrormark host` when its extension connects. The process finds its profile's database file (Section 5), holds a lock on `profiles/<id>.lock` while it runs so that a copied profile cannot drive the same file twice, and exits when the browser closes the connection.

It runs a **round** on connect, whenever the extension reports a change, and whenever `config.yaml` or another member's database file changes, which it checks every 2 seconds. A round:

1. takes the global lock (`flock` on `~/.local/share/mirrormark/lock`);
2. reads `config.yaml` and the database file of every profile that shares a union with this one;
3. computes each union's content (Section 4) and sends this browser the writes it needs;
4. writes this profile's database file, recording only the writes that succeeded, and only if the bookmarks changed (`synced` and `checksum` do not count);
5. releases the lock.

A host writes only its own browser and its own file; other browsers catch up in their next round. Rounds never overlap, so each merges against the latest files. If the browser does not answer for 10 seconds, the round records what succeeded, releases the lock and logs a warning. A crashed host releases its locks with it.

If another member's file is missing, unreadable or fails its checksum, the round skips every union that member belongs to and reports it in `mirrormark status`. It never treats a member it cannot read as empty.

### 2. The protocol

```jsonc
// extension → host, first message; "db" is null until the extension has claimed a database
{"v": 1, "type": "hello", "db": "chrome-personal",
 "browser": {"family": "chromium", "name": "Chrome", "version": "141.0"}}

// host → extension, when "db" was null: the unclaimed databases of this family
{"v": 1, "type": "choose", "candidates": [{"id": "chrome-personal", "name": "Personal"}]}

// extension → host, after hello, after a burst of bookmark events (2 s quiet), after each batch
{"v": 1, "type": "tree", "nodes": [
  {"id": "1", "parent": null, "root": "toolbar"},
  {"id": "57", "parent": "1", "index": 0, "title": "GitHub", "url": "https://github.com/"}]}

// host → extension, at most 1 MB per message
{"v": 1, "type": "apply", "batch": 7, "ops": [
  {"op": "move", "id": "57", "parent": "90", "expect": {"parent": "58"}},
  {"op": "create", "ref": "n1", "parent": "58", "index": 0,
   "title": "GitHub (librewolf)", "url": "https://github.com/orgs/b"}]}

// extension → host, one result per op
{"v": 1, "type": "applied", "batch": 7, "results": [{"ok": true}, {"ok": true, "id": "212"}]}

// either direction
{"v": 1, "type": "error", "code": "db-mismatch", "message": "…"}
```

- **Whole trees.** The extension always sends its whole tree, and the host compares it with the database file, so a missed event is fixed by the next tree.
- **No blind writes.** `update`, `move` and `remove` carry what the host expects to find. The extension reads the node first and fails the op as `stale` if it differs, so an edit the user makes during a batch survives and the next round sees it as a change. Ops run one at a time, as MDN advises when order matters [R5].
- **Versions.** A message with a higher `v` than the receiver knows gets `error` `unsupported-version`. Unknown fields are ignored.

### 3. The YAML files

**One database file per profile**, `~/.local/share/mirrormark/profiles/<id>.yaml`, created by `mirrormark install` and kept current by the host. It holds the profile's identity and its whole bookmark tree as of the last round:

```yaml
# Written by mirrormark. You may edit the profile block; do not edit below it.
format: 1
profile:
  id: chrome-personal              # stable; config.yaml refers to it
  label: chrome/personal           # goes into conflict suffixes
  browser: chrome
  family: chromium
  path: /home/chris/.config/google-chrome/Profile 1
  name: Personal                   # the browser's own name for the profile
  claimed: true
synced: 2026-10-14T09:12:03Z
checksum: sha256:9f2c…             # over everything below this line
unions:
  work: {root: m-4a1e}             # this profile's member folder of union "work"
bookmarks:
  - title: Bookmarks bar
    root: toolbar
    id: "1"
    children:
      - title: Work
        id: "58"
        m: m-4a1e
        v: 1
        children:
          - {title: GitHub, url: "https://github.com/", id: "57", m: m-b7c3, v: 3, by: librewolf}
          - {title: Mail (chrome/personal), suffix: chrome/personal, url: "https://fastmail.com/",
             id: "61", m: m-c9d0, v: 1, by: chrome-personal}
          - title: _deleted
            id: "90"
            m: m-d001
            v: 1
            children:
              - {title: Old wiki, url: "https://wiki.example/", id: "88", m: m-77aa, v: 4, by: librewolf}
purged:
  - {m: m-1234, v: 2, at: 2026-10-01T10:00:00Z}
```

- `id` is the browser's node id. `m` is mirrormark's id, the same in every profile holding the bookmark.
- `v` is the node's version and `by` the profile that made that version (Section 4).
- `suffix` marks a label mirrormark added to the title.
- `purged` lists bookmarks removed from `_deleted`.

**One config file**, `~/.config/mirrormark/config.yaml`, written by the user:

```yaml
format: 1
unions:
  work:                              # union name
    chrome-personal: toolbar/Work    # profile id: folder path
    librewolf: toolbar/Work
  reading:
    chrome-personal: other/Reading
    chrome-work: other/Reading
    librewolf: menu/Reading
```

- A path starts with a top-level folder: `toolbar`, `other`, `menu` (Firefox only) or `mobile`, or the title the browser shows, such as `Bookmarks bar`. `\/` escapes a slash in a title. One profile's paths may not nest.
- The first round creates a missing member folder and records its `m`. After that the host follows the folder by id, not by path. If the folder disappears or leaves the path, the host stops syncing that member and reports it in `status`. A missing member folder never means "everything was deleted". To resume, the user restores the folder or points the config at a new path, which makes the profile a new member (Section 4, First round).

**File safety.**

- **Atomic writes.** Each write goes to a temporary file in the same directory, is flushed to disk, and is renamed over the old file. A crash leaves the old file or the new one, and a reader never sees half a file.
- **Backups.** Before overwriting a database file, the host copies it to `backups/<id>/<time>.yaml` if the newest backup is more than an hour old or this host process has not backed up yet. It keeps the last 20 and the oldest of each of the last 30 days, so a burst of bad rounds cannot rotate away every good copy. `mirrormark export <file>` turns a database or backup into a bookmarks HTML file that both families can import. Backups are for reading and exporting; copying one back over a database file is not a supported recovery.
- **Hand edits.** The host validates `config.yaml` on every read; if it is invalid, nothing syncs, and `status` and the extension's popup show the error. In a database file, the profile block may be edited. The `checksum` covers the rest: if it does not match, the host refuses the file and never overwrites it, so a hand edit is never misread as browser changes. To recover, the user deletes the file and runs `mirrormark install` for that profile, which makes it a new member.

### 4. Union semantics

**A union holds everything any member holds.** A bookmark or folder under any member folder ends up under every member folder, at the same relative path. Each round compares its own browser's live tree with its database file (the last tree) to find what the user changed, and combines that with what the other members' files say.

**Deletion is a move to `_deleted`.** Each member folder has a `_deleted` subfolder, created on first use. When a node leaves a member folder, by being removed or by being moved out of the union, the host:

1. finds it in the last tree, which is what tells a removal apart from a node this profile never had (without it, a union would bring removed bookmarks straight back);
2. recreates it, with its subtree, in that member's `_deleted` (a node moved out of the union also stays where the user put it);
3. records a move into `_deleted`, which reaches every member like any other move.

- **Edits** to a bookmark inside `_deleted` sync like any other edit.
- **Restoring** is moving it back out, in any browser.
- **Purging** is removing a bookmark from `_deleted`. It is the one real deletion, and it is final. The round adds it to `purged` with its version. Each member that applies the purge copies the entry into its own file, so the record survives the loss of any one file. Entries expire after 90 days. A member closed longer than that brings the bookmark back into `_deleted`, which is clutter, not loss. Removing `_deleted` itself purges its bookmarks; the folder is recreated empty.

**Every node in a union has a version `v`.** A change is a new title, URL or parent; a move into `_deleted` counts, and an order-only move does not. In a round for profile P, rules apply in this order:

| Node                                                                     | Other files                                       | Result                                                    |
| ------------------------------------------------------------------------ | ------------------------------------------------- | --------------------------------------------------------- |
| in some file's `purged` at version _n_                                   | none holds it above _n_                           | removed in P                                              |
| in some file's `purged` at version _n_                                   | one holds it above _n_                            | newer version wins; recreated in P, `purged` entry dropped |
| new in P's live tree, no `m`                                             | an unlinked node with the same parent, title and URL (or for a folder, parent and title) | linked to it, one to one, in sibling order |
| new in P's live tree, no `m`                                             | a node new since P's last round with the same parent and title, other URL | **conflict**                         |
| new in P's live tree, no `m`                                             | nothing matching                                  | new node, new `m`, `v` 1                                  |
| in no tree of P's                                                        | present                                           | created in P                                              |
| in P's last tree, gone from P's member folder                            | not above P's version                             | moved to `_deleted` (above)                               |
| in P's last tree, gone from P's member folder                            | above P's version                                 | **conflict** (Open question 1)                            |
| unchanged in P                                                           | not above P's version                             | nothing                                                   |
| unchanged in P                                                           | above P's version                                 | P takes the highest                                       |
| changed in P                                                             | not above P's last version                        | P's change wins; `v` = highest + 1, `by` = P              |
| changed in P                                                             | above, same content                               | P takes it                                                |
| changed in P                                                             | above, different content                          | **conflict**                                              |

Because rounds never overlap, a version above P's is a change P has not seen. A file whose profile has not run for a while holds lower versions and loses to newer ones. The engine checks that one `m` at one `v` always has the same content, and stops with an error if not.

**Conflict: both survive, suffixed.** For a bookmark, the round keeps the existing `m` for the other side's content, retitled `<title> (<label of its by>)`, at `v` = highest + 1. P's content becomes a new node beside it with a new `m`, titled `<title> (<P's label>)`, at `v` 1. Both then spread like any other change. Because `suffix` marks what mirrormark added, a second conflict replaces the suffix instead of stacking `(a) (b)`, and a title the user typed as `Notes (work)` is never touched. Three members whose edits all conflict end with three copies.

| Base                       | Chrome (`chrome/personal`)      | LibreWolf                    | Every member afterwards                                                                |
| -------------------------- | ------------------------------- | ---------------------------- | -------------------------------------------------------------------------------------- |
| `Work/GitHub → github.com` | URL → `github.com/orgs/a`       | URL → `github.com/orgs/b`    | `Work/GitHub (chrome/personal) → …/orgs/a`, `Work/GitHub (librewolf) → …/orgs/b`       |
| `Work/News → lwn.net`      | deleted                         | unchanged                    | `Work/_deleted/News → lwn.net`                                                         |
| `Work/News → lwn.net`      | deleted                         | retitled `LWN`               | `Work/_deleted/News (chrome/personal)`, `Work/LWN (librewolf)` (provisional, Open question 1) |
| (none)                     | adds `Work/Mail → fastmail.com` | adds `Work/Mail → gmail.com` | `Work/Mail (chrome/personal) → fastmail.com`, `Work/Mail (librewolf) → gmail.com`      |

**Folders are never copied**, because a copy would duplicate the subtree. For a folder, every **conflict** row instead keeps the higher version and logs the other (Open question 3). The engine checks the merged tree for cycles (A moves X into Y while B moves Y into X) and undoes the move it is applying in P. When a folder moves to `_deleted` while another member adds a bookmark inside it, the bookmark goes with it: kept, but out of sight until restored.

**First round.** A new member's nodes have no `m`, so they all count as new: identical bookmarks and same-named folders link up, same-titled bookmarks with different URLs become suffixed copies, and the rest is added to every member. A first round deletes nothing. `mirrormark preview <union>` prints what the union would become, from the files, without writing, so the user can check before adding a member to `config.yaml`.

**Wrong-tree guard.** If more than 5 nodes are recorded under a profile's member folders, outside `_deleted`, and fewer than half of their browser ids appear in the live tree, the round writes nothing and answers `error db-mismatch`, shown in the popup and in `status`. This catches the wrong database being claimed, a reset profile, and a browser that has not finished loading its bookmarks. Without it, each of these would move a whole union into `_deleted` everywhere. If the change is real, the user re-runs `mirrormark install` for that profile, which makes it a new member.

**Interrupted rounds.** A round records only writes that succeeded. If a result is lost mid-batch, the next round converges: a lost `create` left a node without `m` that links as in the table (linking runs before the deletion rule and includes `_deleted`); a lost `move` or `update` looks like a change with the same content as a newer version; a lost purge `remove` brings the bookmark back into `_deleted`.

**Room for "ask which side wins".** The engine is one pure function, `merge.Round(live, last Tree, others []Tree, u Union) Result`, returning the ops, the new tree and each conflict with both versions. A later mode would hold a conflict instead of copying it.

### 5. Which profile is this?

Neither the extension nor the host can learn which browser profile the extension runs in [R1] [R2]. So `mirrormark install` creates the database file with the profile's identity, and the extension **claims** it:

- On first connect, `hello` has `"db": null`. The host answers `choose` with the unclaimed databases of the extension's family. The extension shows a badge, and its popup asks the user to pick one, showing each profile's `name` to match against the browser's own profile name, or "none of these", which leaves it idle. It never picks on its own: with Chrome's CRX route (Section 6) the extension runs in every profile. The wrong-tree guard catches a wrong pick.
- The extension keeps the id in `storage.local` and sends it on every connect; the host marks the file `claimed`. If the file is gone or another host holds its lock (a copied profile), the host answers `error` and the popup says why.
- Reinstalling the extension clears `storage.local`. Running `mirrormark install` for that profile again marks its file unclaimed, so the new extension can take it back.

### 6. Installing

The binary embeds both builds of the extension (`go:embed`). `mirrormark install`:

1. lists each browser's profiles: Chromium-family from `Local State`'s `profile.info_cache` (`~/.config/google-chrome/`, `~/.config/chromium/`, `~/.config/BraveSoftware/Brave-Browser/`), Firefox-family from `profiles.ini` (`~/.config/mozilla/firefox/` from Firefox 147 [R15], `~/.mozilla/firefox/`, `~/.librewolf/`);
2. lets the user pick one or more, by number or with `--browser chrome --profile "Profile 1"`;
3. for each pick, unpacks the extension into `~/.local/share/mirrormark/extension/<family>/`, writes the browser's native host manifest, creates the database file, and adds the extension as the browser allows:

| Browser                    | What is possible                                                                                                                                                                                                 | What `install` does |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------- |
| Google Chrome, Linux       | Branded Chrome ignores `--load-extension` since 137 [R12]. A machine-wide preferences file may name a local CRX, which Chrome installs without a prompt [R11], in every profile. Otherwise only "Load unpacked" in Developer mode. | **First choice:** pack a CRX and, with `sudo`, write `/usr/share/google-chrome/extensions/<id>.json` (directory unverified) naming it; the user restarts Chrome. **Fallback:** open `chrome://extensions` in the picked profile and print the steps: Developer mode on, Load unpacked, choose the unpacked folder. |
| Chromium, Brave, Linux     | `--load-extension` works in Chromium (Brave unverified) [R12], for one launch only. Preferences-file directories unverified. | The same two routes, once verified. |
| Firefox release and beta   | Only signed extensions install permanently; Mozilla signs self-distributed XPIs through `web-ext sign` [R13]. Opening an XPI shows the install prompt. | With the profile closed, run `firefox --profile <dir> mirrormark.xpi` (unverified from the command line); the user accepts the prompt. |
| Firefox Developer, Nightly, ESR; LibreWolf | The signed XPI works. Unsigned works with `xpinstall.signatures.required=false` (LibreWolf: conflicting reports [R14]) or until restart via `about:debugging`. | The same as release: one path to test. |

**A key per install.** Chrome derives an extension's id from its public key. `install` generates a fresh key pair, writes the public key into the unpacked extension's manifest `key`, signs the CRX with the private key (a small CRX3 writer using Go's standard crypto), puts the resulting id in the host manifest's `allowed_origins`, and keeps the private key in `~/.local/share/mirrormark/extension.pem` (mode `0600`) so later upgrades keep the id. No signing key is shared or shipped, and an extension built elsewhere does not get this install's id. An upgrade re-signs with the same key and raises `external_version` so Chrome picks up the new build.

`mirrormark uninstall` removes the host manifests, preferences files and unpacked extension, and keeps the YAML files unless given `--purge`. With the CRX route, removing the preferences file removes the extension from every Chrome profile, along with its claim (Section 5).

### 7. Extension and code

The extension is thin: Manifest V3, permissions `bookmarks`, `nativeMessaging` and `storage`, TypeScript compiled by `tsc`. The `Makefile` generates the Chromium and Firefox manifests from one source. The extension connects at startup and on each bookmark event, sends trees, applies ops, and shows the claimed profile, the last round and any error in its popup. Besides `install`, `uninstall`, `preview` and `export`, the CLI has `status` (profiles, claims, unions, last rounds, errors, log path) and `host` (what the native host manifest runs).

One Go module, `github.com/chriscz/mirrormark`. Its only third-party dependency is `go.yaml.in/yaml/v3`, the YAML organisation's continuation of the archived `gopkg.in/yaml.v3` [R16]. Locking uses `syscall.Flock` (Linux and macOS).

As in tab-stash, everything builds and tests in a dev image (`Dockerfile` with Go, Node, Puppeteer, Chrome for Testing and Google Chrome stable; `bin/dev run make`), with the `Makefile` as the one build system.

## Alternatives considered

**One daemon behind a native-messaging relay** (the earlier draft). Each host process relayed to a long-running `mirrormark serve` over a Unix socket, which merged pairs of live trees, with a plan approval step, a journal and a convergence breaker. It needed a daemon that outlives the browser's host, a socket handshake, and both browsers open at once. With the files standing in for the other members, a round needs only its own browser, and a closed browser catches up when it starts.

**A localhost WebSocket server.** Easy to connect to, but any local program or web page can reach a localhost port, so it needs tokens, `Origin` checks and DNS-rebinding defences. Native messaging lets the browser vouch for the extension.

**Editing the browsers' own bookmark files.** Firefox locks `places.sqlite`, and Chromium rewrites `Bookmarks` from memory, so edits made while a browser runs are lost or corrupt the profile.

**One shared file, or SQLite.** A shared file would make every round rewrite every profile's data; SQLite cannot be read by hand. Per-profile files let each host write only its own.

**Real deletes with tombstones.** A mistaken delete would be final everywhere. `_deleted` keeps deletes visible and reversible; tombstones remain only for purges.

**floccus with a local WebDAV server** [R10]. Works across families today, but needs a server and has no suffix merge.

## Tradeoffs and risks

- **`_deleted` grows** in every member until the user empties it.
- **Merge clutter.** Each conflict leaves suffixed copies to tidy, and per-node versions make a title edit on one side and a URL edit on the other a conflict too.
- **One slow browser stalls everyone.** A round holds the global lock while its browser applies ops, up to the 10-second timeout, and every other host waits.
- **The Chrome CRX route needs root and reaches every profile.** Users who refuse `sudo` get the manual fallback, which is tested by hand only.
- **Installation depends on undocumented files** (`Local State`, `profiles.ini`, preferences-file directories) that browsers change; LibreWolf moved its host directory around version 136 [R8].
- **The guard can refuse real changes.** Removing most of a union folder's contents at once trips the wrong-tree guard, and re-running `install` would bring them back from the other members. To clear most of a union, the user moves the bookmarks into `_deleted` rather than removing them.
- **Same-user programs are trusted.** They can rewrite the YAML files, as they could the browsers' own files. The checksum catches accidents, not attacks.

## Rollout

Chrome on Linux first, end to end; the Firefox family follows. Each phase is its own stack of commits, and nothing writes to a browser before Phase 4.

1. **Dev image, YAML database and merge engine.** `Dockerfile`, `bin/dev`, `Makefile`; the file formats, atomic writes, backups, checksum, config validation and `merge.Round`.
   - **Tests:** a table test per row and example in Section 4; property tests over random trees, three members and random round orders (some members closed) checking convergence, no writes after a quiet round, one content per `m` and `v`, and no bookmark lost except by purge.
   - **Exit:** `bin/dev run make` passes.
2. **Chrome extension and host, read-only.** The extension, the host, claiming, and writing the database file. **Tests:** a Go fake extension drives the host; an e2e test in Chrome for Testing adds bookmarks and checks the file. **Exit:** both pass.
3. **`mirrormark install` for Chrome on Linux.**
   - **Tests:** in a throwaway container, `install` runs as root against Google Chrome stable; Chrome then starts as an ordinary user under Puppeteer (without Puppeteer's default `--disable-extensions`), once with a fresh profile and once with a used one.
   - **Exit:** in both runs the extension is enabled, reaches the host with no user action, and its claim produces the database file. The fallback is tried by hand once and recorded in the PR.
4. **Union sync on Chrome.** Writes, `_deleted`, purges, `preview`, `status`, the guard. **Tests:** e2e with two Chrome profiles in one union covering each example in Section 4, a browser closed mid-batch, one browser closed while the other changes, and a wrong claim tripping the guard. **Exit:** all pass, and a run on the developer's Chrome with test profiles is recorded in the PR.
5. **Firefox family.** Signing, the Firefox manifest, `install` for Firefox and LibreWolf. **Exit:** the Phase 4 e2e passes with one Chrome and one Firefox profile in the image.
6. **Packaging.** `make release` builds Linux binaries with both extensions embedded. **Exit:** a fresh Ubuntu container, following only the README, syncs Chrome with Firefox. macOS and Windows follow in their own RFC.

**Rollback.** Remove a member from `config.yaml`, which stops its syncing without touching its bookmarks, or run `mirrormark uninstall`. To undo a bad sync, the user exports a backup and imports it in the browser, then cleans up by hand.

## Success criteria

- Two Chrome profiles (later Chrome and LibreWolf) with a few hundred bookmarks in one union match about 5 seconds after the last edit while both run (2 s quiet, up to 2 s polling, two rounds; measured in Phase 4), and within 5 seconds of a closed one's extension connecting.
- Each example in Section 4 gives the tree shown, in every member.
- In the property tests and the Phase 4 e2e, no bookmark goes missing except by purge, and a round with no user changes writes nothing.
- `mirrormark install` takes a fresh and a used Chrome profile on Linux to a claimed extension using only the steps it prints.

## Security, privacy, operational impact

- **Security.** No network listener; only the extension id in a host manifest can start the host [R1] [R2]. The host validates every message and file and executes nothing it receives. The extension has no host permissions. The `sudo` step writes one JSON file naming a CRX in the user's data directory.
- **Privacy.** The YAML files and backups hold the user's bookmarks in plain text, mode `0600`. Nothing leaves the computer, and there is no telemetry. `uninstall --purge` deletes them.
- **Operations.** About 2 MB per database file per 10,000 bookmarks (estimate), times up to 50 backups. The host logs to `~/.local/state/mirrormark/log`; `status` prints the path.

## Open questions

1. **Delete against edit.** By the version rule, a delete on one side and an edit on the other conflict, leaving a suffixed copy in `_deleted` and a suffixed edited copy in place. Should the edit win instead, leaving nothing in `_deleted`? _Resolve in RFC_; ask the user.
2. **Title edit on one side, URL edit on the other.** A conflict today. Combining them needs a version per field. _Resolve in RFC_; ask the user.
3. **One folder renamed or moved differently on two sides.** Proposed: the higher version wins and the other is logged. Alternative: one folder titled `X (a) / Y (b)`. _Resolve in RFC_; ask the user.
4. **Chromium account and local folders.** A signed-in Chromium profile can have two bookmark bars [R6]. Proposed: paths resolve in the account set when it exists. _Resolve in implementation._
5. **Firefox signing.** Whose addons.mozilla.org account signs the XPI, and where its API keys live. _Resolve in Phase 5._
6. **URL matching.** Should `http`/`https` or a trailing slash count as the same URL? Proposed: no. _Resolve in implementation._

## References

External claims were checked on 2026-10-14 unless marked unverified.

- **[R1]** MDN, "Native messaging" and "Native manifests". https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging
- **[R2]** Chrome, "Native messaging". https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging
- **[R3]** Chrome, "The extension service worker lifecycle". https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle
- **[R4]** Mozilla bug 1770696: open native ports reset the event page's idle timer. https://bugzilla.mozilla.org/show_bug.cgi?id=1770696
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
