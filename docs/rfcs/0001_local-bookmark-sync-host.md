# RFC 0001: Sync bookmarks between browser profiles through a local Go host

- Status: draft
- Author(s): Chris Coetzee, Claude
- Date: 2026-10-14
- Links: none yet. Repository: `github.com/chriscz/mirrormark` (MPL-2.0).

## Problem

People who use more than one browser, such as Brave for one profile and LibreWolf for another, have no local way to keep the bookmarks of two profiles the same. Each browser's built-in sync works only inside its own family and goes through the vendor's servers. Tools that cross families, such as floccus [R10], need a server or cloud storage in the middle.

## Background

**What the user asked for.** mirrormark is a WebExtension plus a program on the user's computer, written in Go and licensed under MPL-2.0. Several browsers with the extension connect to that one program and exchange JSON with it. When a browser connects, it sends its bookmarks. The program then keeps two browsers' bookmark lists the same. When both sides change the same bookmark differently, it merges: the conflicting originals are replaced by copies whose titles end in the source profile's name in brackets, such as `GitHub (brave/personal)` and `GitHub (librewolf)`. Asking the user which side wins is a possible later option, not part of this RFC.

**Two browser families, one bookmarks API.** Firefox and its forks (LibreWolf) and Chromium and its derivatives (Chrome, Brave) all expose `bookmarks.getTree`, `get`, `create`, `update`, `move`, `remove`, and the events `onCreated`, `onRemoved`, `onChanged` and `onMoved` [R5] [R6]. The differences that matter here:

| Concern           | Firefox family                                                                                          | Chromium family                                                                                                                                                              |
| ----------------- | ------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Node ids          | GUID-like strings, stable across restarts, per profile [R5]                                             | small integers as strings, stable across restarts, per profile [R6]                                                                                                          |
| Top-level folders | `toolbar_____`, `menu________`, `unfiled_____` ("Other Bookmarks"), `mobile______` (unverified)         | identified by `folderType` (`bookmarks-bar`, `other`, `mobile`, `managed`) since Chrome 134. A profile may have zero, one or several of each type; `syncing` tells the account copy from the local one [R6] |
| Separators        | yes (`type: "separator"`)                                                                               | no                                                                                                                                                                           |
| Read-only nodes   | the root [R5]                                                                                           | the root and top-level folders; `managed` folders set by policy [R6]                                                                                                         |
| Write limits      | none documented                                                                                         | none; Chrome removed `MAX_WRITE_OPERATIONS_PER_HOUR` [R6]                                                                                                                    |
| Import events     | **not supported**: no `onImportBegan`/`onImportEnded`, and no `onChildrenReordered` either [R11]        | `onImportBegan`/`onImportEnded`; `onChildrenReordered` fires for sorts in the browser's UI [R6]                                                                              |

Ids are local to a profile. The same bookmark has unrelated ids in two profiles, so mirrormark must keep its own mapping between them.

**How an extension can talk to a local program.** There are two ways.

- **Native messaging** [R1] [R2]. The extension calls `runtime.connectNative(name)`. The browser looks up a host manifest `<name>.json` in a per-browser, per-OS location, checks that the extension is listed in it (`allowed_extensions` on Firefox, `allowed_origins` on Chromium), and starts the program it names. Messages are UTF-8 JSON, each preceded by a 32-bit length in native byte order. A message from the host to the extension is limited to 1 MB; the other direction allows 64 MiB on Chromium and 4 GB on Firefox. **The browser starts one host process per port**, and the process lives until the port closes. When the port closes, the browser kills the host: on Linux and macOS with `SIGTERM` then `SIGKILL`, which reach any child that has not left the process group; on Windows by killing a job object, which takes every child not started with `CREATE_BREAKAWAY_FROM_JOB` [R1]. An open native port keeps a Chromium MV3 service worker alive (Chrome 105+) [R3] and resets the idle timer of a Firefox event page (Mozilla bug 1770696) [R4].
- **A local network server.** The program listens on `127.0.0.1` and the extension opens a WebSocket or makes HTTP requests to it. An active WebSocket keeps a Chromium service worker alive (Chrome 116+) [R3].

**Where browsers look for host manifests.** Each browser, and sometimes each fork, has its own directory, so installing a host means writing one manifest per browser:

| Browser   | Linux, per user                                                    | macOS, per user                                                          | Windows                                                                  |
| --------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------------------------------------ |
| Firefox   | `~/.mozilla/native-messaging-hosts/` [R1]                          | `~/Library/Application Support/Mozilla/NativeMessagingHosts/` [R1]       | registry `HKCU\Software\Mozilla\NativeMessagingHosts\<name>` [R1]        |
| LibreWolf | `~/.librewolf/native-messaging-hosts/` [R8]                        | `~/Library/Application Support/LibreWolf/NativeMessagingHosts/` [R8]     | the Mozilla registry key [R8]                                            |
| Chrome    | `~/.config/google-chrome/NativeMessagingHosts/` [R2]               | `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/` [R2] | registry `HKCU\Software\Google\Chrome\NativeMessagingHosts\<name>` [R2]  |
| Brave     | `~/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/` [R9] | unverified                                                               | unverified                                                               |

The LibreWolf and Brave paths come from third-party reports, not vendor documentation.

**Prior art.** floccus is an MPL-2.0 extension that syncs native bookmarks across Firefox and Chromium browsers through a backend such as WebDAV, Nextcloud, Git or Google Drive [R10]. It shows that cross-family bookmark sync through the bookmarks API works. It differs from mirrormark in needing storage outside the computer, and in how it resolves conflicts (not checked here).

## Goals

1. **Two profiles stay in sync.** After a sync, two paired profiles hold the same bookmarks: same folders, titles and URLs, in the same order where the browsers allow it.
2. **Any mix of families.** Pairs work across Firefox-family and Chromium-family browsers in any combination: LibreWolf and Brave, Firefox and Firefox, Chrome and Brave.
3. **Conflicting bookmarks are merged, never dropped.** When both sides changed the same field of the same bookmark differently (title, URL or folder), both versions survive in both profiles, each titled with its source profile's label, and they replace the original. Three cases deliberately differ, because only one version exists or because copying would duplicate a subtree: an edit against a delete keeps the edit; a folder renamed on both sides stays one folder; a title edit on one side and a URL edit on the other combine. Section 4 gives each rule.
4. **No bookmark is lost to a mistake.** Every write is preceded by a backup, the first sync of a pair is a dry run the user approves, a sync that would delete many bookmarks stops and asks, and a write never overwrites a change the host has not seen.
5. **Everything stays on the computer.** No server, no account, no network listener.
6. **One host, many browsers.** Any number of browsers connect to one running host at the same time.
7. **Room for "ask which side wins".** The merge engine reports conflicts in a form a later resolution mode can use without a rewrite.

## Non-goals

- **Asking the user which side wins.** Today mirrormark only merges. Section 4 shows where the option fits later.
- **More than two profiles in a sync group.** A profile belongs to at most one pair. Chains (A–B and B–C) and groups of three or more are a later RFC, probably with the hub design under Alternatives.
- **Syncing while one side is closed.** A pair syncs only while both profiles are connected. Section 7 explains why, and Open question 1 asks whether that is enough.
- **Syncing between computers.** The host and both browsers run on one machine.
- **Tabs, history, passwords or settings.** Bookmarks only.
- **Bookmark metadata beyond title, URL, folder and position.** Tags, keywords and descriptions (Firefox) and `dateLastUsed` (Chromium) are not synced, because the other family has no place to put them.
- **Separators across families.** Chromium has none. Firefox separators stay in Firefox and are never sent to a Chromium profile.
- **Browser stores and signed releases.** Publishing to addons.mozilla.org or the Chrome Web Store is decided after the design works (Open question 8).
- **Sandboxed browsers.** Flatpak and Snap builds restrict native messaging. They are not supported until someone tests them.
- **A GUI for the host.** The host has a command line. The extension's options page shows status and the profile label.

## Proposal

We propose a Go host, a thin extension, and a merge engine that compares each profile against the last state both sides agreed on.

```
 Brave (personal)                      LibreWolf
 ┌──────────────┐                      ┌──────────────┐
 │  extension   │                      │  extension   │
 └──────┬───────┘                      └──────┬───────┘
        │ native messaging (stdio)            │ native messaging (stdio)
 ┌──────┴────────────┐                 ┌──────┴────────────┐
 │ mirrormark        │                 │ mirrormark        │
 │   native-host     │                 │   native-host     │      mirrormark CLI
 └──────┬────────────┘                 └──────┬────────────┘      (pair, plan, …)
        │ Unix socket (same frames)           │                         │
        └──────────────┬──────────────────────┴─────────────────────────┘
               ┌───────┴──────────┐   state dir: profiles, pairs,
               │ mirrormark serve │── bases, id maps, in-flight batches,
               │   (daemon)       │   backups, journal, log
               └──────────────────┘
```

### 1. Transport: native messaging, relayed to one daemon

The browser starts a separate host process for each port, so two browsers mean two processes. To let them share state, one Go binary has two roles:

- **`mirrormark native-host`** is what the host manifest names. The browser starts it. It connects to the daemon's socket and copies frames in both directions without parsing them. If nothing answers on the socket, it starts `mirrormark serve` in a new session (`setsid` on Linux and macOS, `CREATE_BREAKAWAY_FROM_JOB` on Windows), so that the browser killing the shim does not kill the daemon [R1]. It waits up to 5 seconds for the socket, then connects. A lock file in the state directory stops two shims from starting two daemons. The shim exits when either side closes: stdin closing means the browser closed the port, and the socket closing means the daemon died or restarted. Exiting on a dead daemon closes the browser's port, so the extension's reconnect starts a fresh shim and daemon. If the daemon cannot be started, the shim sends one `error` frame (`daemon-start-failed`) to the extension before exiting, so the options page can say why.
- **`mirrormark serve`** is the daemon. It listens on a Unix socket in a directory with mode `0700`: `$XDG_RUNTIME_DIR/mirrormark/` on Linux, falling back to `/tmp/mirrormark-<uid>/` (checked for ownership) when that variable is unset, and `$TMPDIR/mirrormark/` on macOS. Socket paths are limited to about 104 bytes, so the daemon refuses to start with a clear error if the path is longer. Windows is Open question 6. The daemon holds the connected profiles, runs the merge engine and writes all state to disk, so it can exit when idle and start again without losing anything. It exits 10 minutes after the last client disconnects.
- **Versions.** The shim's first frame on the socket names its binary version. A daemon from an older binary finishes any batch in flight, then exits; the shim's connection closes, and the next reconnect starts the new daemon. This keeps an upgraded install from talking to a stale daemon for up to 10 minutes.

Both sides of the socket use the native-messaging framing: a 4-byte length in native byte order (`binary.NativeEndian` in Go [R7]), then UTF-8 JSON. The shim does not need to understand the protocol. The `mirrormark` command-line tool talks to the same socket (Section 9), starting the daemon the same way if needed.

**Why not a localhost server.** See Alternatives. In short, native messaging lets the browser decide which extension may talk to the host, so mirrormark needs no network listener, no pairing token and no origin checks.

### 2. Profiles and labels

A profile is one browser profile with the extension installed.

- **Identity.** On first run the extension generates a random UUID and keeps it in `storage.local`. The host keys everything by this id. Extensions cannot read the browser's profile name, so the id is the only stable handle.
- **Label.** The label is the text that goes inside the brackets in conflict titles. The user sets it on the extension's options page, and the extension suggests a default: the browser name, lowercased (`librewolf`, `brave`). On Firefox-family browsers the name comes from `runtime.getBrowserInfo()`. On Brave it comes from detecting `navigator.brave`. Otherwise it falls back to `chrome` or `firefox`. A user with two profiles in one browser adds a profile name: `brave/personal`, `brave/work`. Labels must be unique among known profiles. The host rejects a duplicate, and the options page shows the error.
- **Reinstalling or clearing extension data.** Either deletes `storage.local`, so the extension shows up as a new profile. The old profile's pair stops syncing, and the user pairs the new one. Its first sync is a dry run, so this cannot silently duplicate or delete anything.
- **Copied profiles.** Copying a browser profile directory copies the UUID too. If a second connection arrives with an id that is already connected, the host rejects it with `duplicate-profile`, and the options page offers "use a new identity", which generates a fresh UUID.

### 3. The protocol

Every message is a JSON object with a version and a type. Six types carry the extension's traffic:

```jsonc
// extension → host, first message on every connection
{"v": 1, "type": "hello", "profile": "7f1c…", "label": "brave/personal",
 "browser": {"family": "chromium", "name": "Brave", "version": "141.0"},
 "extension": "0.1.0"}

// extension → host, after hello, after every burst of bookmark events,
// and after every batch
{"v": 1, "type": "tree", "seq": 42, "nodes": [
  {"id": "1", "parent": null, "root": "toolbar", "syncing": false},
  {"id": "57", "parent": "1", "index": 0, "kind": "bookmark",
   "title": "GitHub", "url": "https://github.com/"},
  {"id": "58", "parent": "1", "index": 1, "kind": "folder", "title": "Work"}]}

// host → extension; at most 1 MB per message, so large batches are split
{"v": 1, "type": "apply", "batch": "b-0193…", "based_on": 42, "part": 1, "parts": 1, "ops": [
  {"op": "update", "id": "57", "title": "GitHub (brave/personal)",
   "expect": {"title": "GitHub", "url": "https://github.com/orgs/a"}},
  {"op": "create", "ref": "n1", "parent": "1", "index": 1, "kind": "bookmark",
   "title": "GitHub (librewolf)", "url": "https://github.com/orgs/b"}]}

// extension → host, one result per op
{"v": 1, "type": "applied", "batch": "b-0193…", "part": 1,
 "results": [{"ok": true}, {"ok": true, "id": "212"}]}

// host → extension, for the options page
{"v": 1, "type": "status", "paired_with": "librewolf", "state": "synced",
 "last_sync": "2026-10-14T09:12:03Z", "pending_approval": false}

// either direction
{"v": 1, "type": "error", "code": "label-taken", "message": "…"}
```

- **Full trees, not deltas.** The extension always sends its whole tree. The host works out what changed by comparing against the stored base (Section 4). A tree of 10,000 bookmarks is about 1–2 MB of JSON (estimate, to be measured in Phase 2), well under the 64 MiB and 4 GB limits for messages to the host. A missed event or a restart is fixed by the next tree.
- **Top-level folders are named, not numbered.** The extension maps its browser's top-level folders to `toolbar`, `menu`, `other` and `mobile`, and reports `syncing` on Chromium (Section 5).
- **Every write states what it expects to find.** `update`, `move` and `remove` carry an `expect` object: the title and URL for `update`, the parent for `move`, and title, URL and parent for `remove`. Before each op, the extension reads the node with `bookmarks.get` and compares. If the node no longer matches (the user edited it after the tree was sent), the op fails with `{"ok": false, "code": "stale"}` and nothing is written. `remove` never uses `removeTree`; the host removes a folder's children first and removes the folder only when it is empty, so a child the user added meanwhile makes the folder's `remove` fail as stale instead of deleting the child.
- **Failed ops.** Any op can fail (`stale`, `not-found`, or a browser error). The extension carries on with the rest of the batch, and any later op that refers to a failed `ref` fails as `missing-ref`. Section 6 says what the host does with failures.
- **`ref` links new nodes.** A `create` carries a host-chosen `ref`, and the extension returns the browser's new id for it, so later ops in the same batch and the host's id map can refer to it.
- **`based_on`** names the tree `seq` the batch was computed from, for the log and for debugging.
- **Unknown fields are ignored** and `v` is checked: a host that receives a higher `v` than it knows answers with `error` `unsupported-version` and closes the connection.

### 4. The merge engine

The engine is a pure Go package with no I/O:

```go
// Package merge computes the changes that bring two bookmark trees together.
func Merge(base Tree, a, b Side, opts Options) Result

type Side struct {
    Profile ProfileID
    Label   string // e.g. "brave/personal"
    Tree    Tree   // the profile's tree, ids mapped to pair ids
}

type Result struct {
    OpsA, OpsB []Op      // writes for each browser
    Target     Tree      // what both sides hold after the ops apply
    Conflicts  []Conflict
    Stats      Stats     // creates, updates, moves, deletes per side
}

type Conflict struct {
    Kind       ConflictKind // EditEdit, MoveMove, AddAdd, EditDelete, FolderRename, Cycle
    Node       PairID
    A, B       *Node        // each side's version; nil if deleted
    Resolution Resolution   // MergeWithSuffix, KeepEdit, CombineTitle, KeepFirstPaired
}
```

**Three-way merge against a stored base.** For each pair, the host stores the base: the tree both profiles held after their last complete sync, and a map from each base node's pair id to its id in profile A and in profile B. Without it, the host cannot tell a bookmark A deleted from a bookmark B added. With it, each side's changes since the last sync are a diff.

**Matching.** Nodes in the id map match by id. Nodes not in it (the first sync, and nodes added on both sides since the last sync) match by key: for bookmarks (parent, title, URL), for folders (parent, title). When one side has several unmapped siblings with the same key, they pair up in index order, and any left over count as plain adds.

**Per-field rules.** A bookmark has three fields that sync: title, URL and parent folder. Position within the folder is handled separately (Order, below). For each mapped node, the engine compares each field on each side with the base:

| A since base                   | B since base                           | Result                                                                       |
| ------------------------------ | -------------------------------------- | ---------------------------------------------------------------------------- |
| changed field F to X           | unchanged                              | set F to X on B                                                              |
| changed field F to X           | changed the same field F to X          | nothing to do                                                                |
| changed field F to X           | changed a different field G            | both changes apply on both sides (combine)                                   |
| changed title or URL to X      | changed the same field to Y            | **edit/edit conflict** on a bookmark: merge with suffix                      |
| changed parent to P            | changed parent to Q                    | **move/move conflict**: a bookmark merges with suffix; a folder takes the first-paired profile's parent |
| changed anything               | deleted                                | **edit/delete**: keep the changed version, recreate it on B, no suffix        |
| deleted                        | deleted                                | gone on both, nothing to do                                                  |
| deleted                        | unchanged                              | delete on B (subject to the mass-delete guard)                               |
| absent (unmapped)              | absent (unmapped)                      | see Matching: same key links up; otherwise each is created on the other side |
| added bookmark at (parent, T), URL U | added bookmark at (parent, T), URL V | **add/add conflict**: merge with suffix                                 |
| renamed folder to X            | renamed the same folder to Y           | **folder rename**: one folder, titled `X (A's label) / Y (B's label)`         |
| a move that makes a folder its own descendant | (the other side's move)   | **cycle**: the first-paired profile's move wins, the other is dropped and reported |

"Changed anything" includes moving the node, editing it, and, for a folder, anything added, changed or moved into its subtree.

**Merge with suffix, today's only conflict resolution.** For edit/edit, move/move and add/add on bookmarks, both versions survive as separate bookmarks:

- A's version is retitled `<A's title> (<A's label>)`, and B's version `<B's title> (<B's label>)`.
- Both appear in both profiles, each at the position its own side gave it. On each side, the existing node is updated into its own suffixed version, keeping its browser id, and the other side's version is created next to it. In the new base they are two ordinary nodes with their own pair ids, so a later edit or delete of either syncs normally.
- The base records which titles carry a suffix mirrormark added. Before suffixing again, the engine strips only a suffix it added itself, so a bookmark that conflicts twice reads `GitHub (librewolf)`, not `GitHub (librewolf) (brave/personal)`, and a title the user typed as `Notes (work)` is never touched.

Suffixed copies apply to bookmarks only. A folder is never duplicated, because copying it would copy its whole subtree.

**Worked examples.** Each starts from the base on the left; both profiles end with the tree on the right.

| Case      | Base                         | Brave (`brave/personal`)              | LibreWolf (`librewolf`)          | Both profiles after sync                                                                    |
| --------- | ---------------------------- | ------------------------------------- | -------------------------------- | ------------------------------------------------------------------------------------------- |
| edit/edit | `Toolbar/GitHub → github.com`| URL → `github.com/orgs/a`             | URL → `github.com/orgs/b`        | `Toolbar/GitHub (brave/personal) → …/orgs/a`, `Toolbar/GitHub (librewolf) → …/orgs/b`       |
| move/move | `Toolbar/Docs → docs.rs`     | moved to `Toolbar/Rust/`              | moved to `Other/Reading/`        | `Toolbar/Rust/Docs (brave/personal) → docs.rs`, `Other/Reading/Docs (librewolf) → docs.rs`  |
| add/add   | (none)                       | adds `Toolbar/Mail → fastmail.com`    | adds `Toolbar/Mail → gmail.com`  | `Toolbar/Mail (brave/personal) → fastmail.com`, `Toolbar/Mail (librewolf) → gmail.com`      |
| edit/delete | `Toolbar/News → lwn.net`   | retitled to `LWN`                     | deleted                          | `Toolbar/LWN → lwn.net`                                                                     |
| combine   | `Toolbar/Go → go.dev`        | title → `Go home`                     | URL → `go.dev/doc`               | `Toolbar/Go home → go.dev/doc`                                                              |
| folder rename | `Toolbar/Work/`          | renamed `Job`                         | renamed `Office`                 | `Toolbar/Job (brave/personal) / Office (librewolf)/`, children merged                        |

**Folder deletes are not recursive overrides.** When A deletes a folder and B changed something inside it, the folder survives on both sides, holding only what B changed: bookmarks B added, edited or moved into it. Everything inside it that B did not touch is deleted on B, as A asked. A bookmark B moved out of the folder before A's delete arrives is a changed node, so it survives at its new place. If nothing in the folder survives, the delete goes through.

**Order.** Positions are best effort and never create a suffixed copy. The engine keeps each folder's base order, applies one-sided moves within a folder, and places new nodes after the sibling that preceded them on their source side. If both sides reordered the same folder, the first-paired profile's order wins. Firefox separators are ignored when computing Chromium indexes and stay where they are in Firefox.

**First sync.** A new pair has an empty base, so everything is unmapped and the Matching rules apply: identical bookmarks at the same path link up, folders with the same path merge, and same path and title with different URLs become suffixed pairs. Bookmarks with the same URL in different folders stay as two bookmarks; mirrormark does not deduplicate. The first sync is always a dry run (Section 6).

**Where "ask which side wins" fits later.** Every `Conflict` carries both versions and a `Resolution`. Today `Options.Resolve` returns `MergeWithSuffix` for bookmark conflicts and the fixed resolutions in the table for the rest. A later mode would return `Pending` for some conflicts. The engine then leaves those nodes in the new base at their base version and emits no ops for them, so on the next run they are still in conflict rather than reappearing as adds. The host sends them to the options page, and the user's answer comes back as `KeepA` or `KeepB` on the next run. That needs new `Resolution` values and a UI, not new types.

### 5. Mapping top-level folders between families

The extension translates its browser's top-level folders to four names:

| Name      | Firefox family                     | Chromium family                 |
| --------- | ---------------------------------- | ------------------------------- |
| `toolbar` | `toolbar_____`                     | `folderType: "bookmarks-bar"`   |
| `other`   | `unfiled_____` ("Other Bookmarks") | `folderType: "other"`           |
| `mobile`  | `mobile______`                     | `folderType: "mobile"`          |
| `menu`    | `menu________`                     | none                            |

- **Same family.** Two profiles of the same family map one to one, so a Firefox–Firefox pair keeps `menu` as a top-level folder.
- **Chromium account and local folders.** A signed-in Chromium profile can hold two folders of the same type, one saved to the account (`syncing: true`) and one local [R6]. mirrormark syncs the account set when it exists and the local set otherwise, never both. The other set is reported in `plan` and never written to. Open question 3 asks whether that is the right default.
- **Firefox's menu on Chromium.** Chromium has no `menu`. Its contents go into a folder titled `Bookmarks Menu` inside Chromium's `other`. The host identifies that folder by its pair id, not by title, so a folder the user already had with that name is just an ordinary folder. The host treats the stand-in like a top-level folder: renaming or moving it on Chromium is undone on the next sync, and deleting it is reported and the folder is recreated empty. Its contents sync normally.
- **Read-only nodes.** Chromium's `managed` folder, and any node the browser reports as unmodifiable, are sent as read-only. The engine never writes to them and never sends them to the other side.
- **Older Chromium.** `folderType` arrived in Chrome 134. mirrormark requires Chromium 134 or later rather than guessing from ids.

### 6. Data safety

mirrormark edits people's bookmarks, so every write path has a guard.

- **Backup before every write.** Before the host sends an `apply`, it writes both profiles' current trees to `backups/<pair>/<timestamp>.json.gz`, unless the trees are identical to the previous backup. It keeps the last 50 backups and one a day for 30 days. At about 300 KB per compressed 10,000-bookmark tree (estimate), that is at most about 50 MB per pair. `mirrormark export <backup>` writes any backup as a Netscape bookmarks HTML file, which both browser families can import by hand.
- **A journal of every removal and retitle.** Every node mirrormark deletes or retitles is appended, in full, to a journal. `mirrormark restore <entry>` recreates one entry through the normal `apply` path.
- **Whole-tree restore.** `mirrormark restore --backup <backup> --profile <label>` pauses the pair, computes the ops that turn the profile's live tree back into the backup, shows them as a plan, and applies them after approval. The user restores both profiles from the same backup and then runs `mirrormark resume <pair>`, which resets the base to the restored tree.
- **The first sync is a dry run.** `mirrormark pair <a> <b>` computes the first merge and prints it: counts per side, every conflict, every top-level change, and a plan id. Nothing is written until the user runs `mirrormark approve <pair> <plan-id>`. The plan id is a hash of the base and both trees, so if either profile changed since the plan was printed, `approve` refuses and prints the new plan. `mirrormark plan <pair>` shows the current plan at any time.
- **Mass-delete guard.** If a sync would delete more than 25 bookmarks, or more than 10% of either profile's bookmarks and at least 5, the host writes nothing, marks the pair `needs-approval`, and shows the plan. This catches a reset profile that suddenly sends an empty tree. Both limits are settings.
- **No blind overwrites.** Every `update`, `move` and `remove` carries `expect` (Section 3), so a user edit made after the tree was sent is never overwritten.
- **Interrupted batches converge.** Before sending a batch, the host writes it to the store as the pair's in-flight batch, with the target tree. It records each `applied` result as it arrives. Until both sides' ops have each either succeeded or failed, the pair is `applying`. When a side's next tree arrives during `applying`, for example after the browser closed mid-batch:
  1. Nodes created by a `create` whose result was lost are linked to their `ref` by key (parent, title, URL).
  2. Ops whose effect is already visible in the tree count as done.
  3. The remaining ops are re-sent, with the same `expect` checks.
  4. An op that fails is dropped. The node keeps whatever the user made of it.

  When no op is left, the target becomes the new base, and any difference between a side and that base (a dropped op, or an edit the user made meanwhile) is an ordinary change on the next sync. A half-applied conflict therefore cannot replay as a fresh conflict and create duplicate suffixed copies.
- **Echoes are harmless.** Applying a batch fires `onCreated`, `onChanged` and so on. The extension does not drop those events; it holds them until the batch ends, then sends one tree. That tree also carries any edit the user made during the batch. Because the host diffs state against the base, a tree that matches the new base produces no ops.
- **Convergence check.** If both trees a sync receives equal the previous target, the merge must produce no ops. If it produces any, that is a bug: the host stops syncing the pair, marks it `stalled`, and logs both trees and the ops. Separately, more than 30 syncs with writes in one minute pause the pair for a minute and log a warning.
- **Paced writes.** The extension applies ops one at a time, waiting for each, as MDN advises when index order matters [R5]. The daemon splits a batch into parts of at most 500 ops and checks that each encoded frame stays under 1 MB.
- **Imports.** On Chromium, the extension sends nothing between `onImportBegan` and `onImportEnded`, then sends one tree. Firefox has no import events [R11], so an import there arrives as a stream of `onCreated` events. The debounce and the first-sync-style matching handle it, and the mass-delete guard does not apply because imports only add.

### 7. When syncing happens

- **On connect.** When a profile sends `hello` and its first `tree`, and its partner is connected, the host syncs the pair.
- **On change.** The extension listens to `onCreated`, `onRemoved`, `onChanged` and `onMoved`, and on Chromium also `onChildrenReordered`. It waits until 2 seconds pass without another event and sends its tree. The host syncs the pair if the partner is connected.
- **Not on a timer.** Full trees on connect and on change cover everything a timer would. A periodic sync would only add writes.

A pair syncs only while both profiles are connected. Writing to a browser needs its extension, and an extension only runs while its browser does. The host could merge against the partner's last known tree and queue writes for when it returns, but then each side would have its own base, which complicates the engine and its guarantees. If one browser is closed, its changes wait until both are open. Open question 1 asks whether that is enough.

### 8. The extension

The extension is thin. All logic lives in the host.

- **Permissions:** `bookmarks`, `nativeMessaging`, `storage` and `alarms`. No host permissions.
- **Manifests:** Manifest V3 on both families. One source manifest, from which the `Makefile` generates the Firefox-family manifest (`background.scripts`, an event page, and a fixed `browser_specific_settings.gecko.id`, `mirrormark@chriscz.github.io`) and the Chromium-family manifest (`background.service_worker`, and a `key`, so the unpacked extension's id is fixed and can be listed in `allowed_origins`).
- **Connection:** opens one `connectNative("io.github.chriscz.mirrormark")` port at startup. The open port keeps the background alive on both families [R3] [R4]. When the port closes, the extension reconnects on the next bookmark event, on `runtime.onStartup`, and from a one-minute `alarms` alarm, because a stopped Chromium service worker or unloaded Firefox event page loses its timers.
- **Options page:** the label, connection and pair status, the last sync, the error from the host if any, and a notice when a pair needs approval in the host.
- **Language:** TypeScript compiled by `tsc` from the `Makefile`, with no bundler.

### 9. The host program and installation

One Go module, `github.com/chriscz/mirrormark`, one binary, standard library only where possible:

```
cmd/mirrormark/          subcommands
internal/nmframe/        length-prefixed JSON framing
internal/protocol/       message types, for the extension and the CLI
internal/tree/           the canonical tree and top-level mapping
internal/merge/          the engine (Section 4), no I/O
internal/store/          state: profiles, pairs, bases, in-flight batches, backups, journal
internal/daemon/         socket server, sessions, sync scheduling
internal/install/        host manifests per browser and OS
```

The command-line tool connects to the daemon's socket and sends requests of its own types (`cli.profiles`, `cli.pair`, `cli.plan`, `cli.approve`, `cli.status`, `cli.restore`, …), starting the daemon if it isn't running. Only the daemon writes the state directory, so the CLI and the browsers never race on it.

| Command                                             | What it does                                                         |
| --------------------------------------------------- | -------------------------------------------------------------------- |
| `mirrormark install [--browser=…]`                  | writes the host manifest for each detected browser, and lists them   |
| `mirrormark uninstall [--purge]`                    | removes them; `--purge` also deletes the state directory             |
| `mirrormark profiles`                               | lists known profiles: id, label, browser, connected, last seen       |
| `mirrormark pair <a> <b>` / `unpair <pair>`         | creates or removes a pair; profiles named by label or id             |
| `mirrormark plan <pair>` / `approve <pair> <plan>`  | shows or approves the pending merge                                  |
| `mirrormark pause <pair>` / `resume <pair>`         | stops and restarts syncing a pair                                    |
| `mirrormark status`                                 | pairs, their state, last sync and its latency, and the log's path    |
| `mirrormark backups` / `export <backup>`            | lists backups; writes one as bookmarks HTML                          |
| `mirrormark restore <entry>` / `restore --backup …` | restores one journal entry, or a whole profile from a backup         |
| `mirrormark serve` / `native-host`                  | the daemon and the shim                                              |

`install` knows each browser's manifest location (Background). It writes the Firefox-family manifest with `allowed_extensions` and the Chromium-family one with `allowed_origins`, from one template in the binary. On Windows it writes the registry keys. State lives in the OS's user state directory (`$XDG_STATE_HOME/mirrormark` on Linux), with files mode `0600`.

Releases are built by the `Makefile` and attached to GitHub releases: Go binaries for Linux, macOS and Windows, and the two extension packages.

## Alternatives considered

**A localhost WebSocket or HTTP server.** The daemon listens on `127.0.0.1`, and every extension connects to it directly. Its real strength is installation: no shim and no per-browser host manifests, which removes this RFC's biggest operational risk. But any program on the computer, and any web page the user visits, can try to connect to a localhost port. The daemon would need a pairing token copied into each extension, a check of the `Origin` header, and protection against DNS rebinding, and would still accept any local process holding the token. The extension would also need a host permission for `127.0.0.1`, which Firefox's MV3 lets the user decline or withdraw (unverified for current versions). Native messaging gets the same result with the browser vouching for the extension's id and no listening port.

**No daemon: each host process syncs on its own.** Each shim would load the state from disk under a file lock and run the sync itself. But a sync needs both profiles' live trees and a way to write to both, and each shim holds only one browser's port. The two processes would have to pass trees and ops through files and wait for each other, which rebuilds the daemon badly. A variant where the first shim becomes the server for the rest has to hand over when that browser closes, mid-sync.

**Edit the browsers' bookmark files directly.** Firefox keeps bookmarks in `places.sqlite` and Chromium in a `Bookmarks` JSON file, so a Go program could sync them without any extension. Both browsers hold these files open and overwrite them while running: Firefox locks the database, and Chromium rewrites its file from memory, so edits made while the browser runs are lost or corrupt the profile. Syncing would only be safe with both browsers closed, which defeats the point.

**A shared canonical tree (hub) instead of pairs.** The host keeps one master tree, and every profile syncs against it. This is the natural design for three or more profiles and for syncing while a browser is closed. It also means deciding which master changes reach which profile, and how suffixes work with many labels. The user asked for two browsers today, and pairs answer that with a smaller engine. A hub is the likely shape of the N-profile RFC, and `Merge(base, a, b)` can serve it with the hub as one side.

**Last-writer-wins by timestamp.** Pick the newer version using `dateAdded`/`dateGroupModified`. Browsers don't record when a bookmark's title or URL changed, only folder modification times. It would also silently drop one side's edit, which the user's merge rule exists to prevent.

**floccus with a local WebDAV server.** floccus already syncs native bookmarks across families [R10]. Running a WebDAV server on the computer would make it local. It would still need a server, and it does not offer the bracket-suffix merge the user wants. Contributing that mode to floccus is a reasonable fallback if mirrormark stalls.

## Tradeoffs and risks

- **Installation touches many directories.** Every browser and fork has its own host manifest location, and forks change theirs: LibreWolf moved from `~/.mozilla` to `~/.librewolf` around version 136 [R8]. `install` will miss browsers it doesn't know, and a moved directory breaks the connection silently. Mitigation: `install` lists what it found and wrote, `mirrormark status` shows profiles that have never connected, and the options page says "host not found" when `connectNative` fails.
- **Merging creates visible clutter.** Every conflict leaves two suffixed bookmarks the user must tidy by hand, and every edit/delete conflict brings back something the user deleted. That is the price of never losing data with a merge-only strategy. The first sync between two long-used profiles may suffix many common titles such as "Home". The dry run shows each conflict before anything is written, and the later "ask" mode is the real fix.
- **Pair-only syncing.** If the user mostly uses one browser at a time, the pair rarely syncs. Changes are not lost, because the base stays put until both are connected, but they wait. If this turns out to be the common case, the hub design moves forward.
- **Bookmark names change.** Conflict suffixes change titles the user typed, and they travel to every device the browser's own sync reaches. A label typo becomes visible everywhere, and renaming a label does not rename existing suffixes.
- **Interaction with browser sync.** If Brave Sync or Firefox Sync is also on, a change mirrormark writes is also pushed by the browser to other devices, and changes from those devices arrive as ordinary events. mirrormark treats them like local edits. Two sync systems acting on one profile can amplify a bad merge, which is why the mass-delete guard and the convergence check exist.
- **Re-sending the full tree on every change.** A 10,000-bookmark profile sends 1–2 MB after every burst of edits, and the host diffs it. This is cheap on a desktop but unmeasured. Phase 2 measures it; if it is too slow, the extension can send a hash first.
- **One daemon is a single point of failure.** If it crashes, every shim exits, the extensions reconnect, and the next shim starts a new daemon. State on disk and the in-flight batch record make this a delay, not a loss.
- **Same-user processes are trusted.** Any program running as the user can connect to the socket, push an empty tree, or approve a plan. That program could already edit the browsers' bookmark files, so this adds no new power, but it does make a mass delete a single message away. The mass-delete guard applies to every source, including this one.

## Rollout

Each phase is its own stack of commits and lands on `main` independently. Nothing writes bookmarks before Phase 3b.

**Phase 1: the merge engine.**

- **Work:** `internal/tree`, `internal/merge` and the store's base format.
- **Tests:**
  - table tests for every row of Section 4's rules and every worked example, the suffix rules, folder deletes, cycles, matching of duplicate siblings, and the top-level mapping including the Chromium account/local case and the `Bookmarks Menu` stand-in;
  - property tests over random base, A and B:
    - applying `OpsA` to A and `OpsB` to B gives two trees that equal `Target` after projecting each onto its family (separators dropped on Chromium, `menu` mapped, read-only nodes dropped);
    - merging the results again yields no ops;
    - nothing is lost (as defined under Success criteria);
    - cutting either op list at every position, then re-running the in-flight reconciliation, converges to the same `Target` with no duplicate nodes;
  - a Go fuzz test runs the same properties.
- **Exit criteria:** `make` passes, including `go test ./...` and `go vet`.

**Phase 2: daemon, shim, extension, read-only.**

- **Work:**
  - the transport: framing, protocol, daemon, shim and the CLI channel;
  - the commands: `install` (Linux), `profiles`, `pair` and `plan`;
  - the extension and its generated manifests. The extension sends trees but never applies ops.
- **Tests:** integration tests drive the daemon with a fake extension written in Go, including a daemon killed under a connected shim. A fixed pair of exported trees with a hand-checked expected plan is the golden test for `plan`.
- **Exit criteria:** with LibreWolf and Brave on Linux, both profiles appear in `mirrormark profiles`. `mirrormark plan` on the golden pair matches the expected plan, and on the developer's real profiles it shows no change the developer cannot explain. Tree size and diff time for the largest real profile are recorded in this RFC.

**Phase 3a: backups and journal.** Backups, `export`, the journal and the in-flight batch record, still without writing to browsers. **Exit:** every backup's HTML export imports into both browsers.

**Phase 3b: writes.**

- **Work:** `apply` with `expect`, `approve`, `pause`/`resume`, `restore`, the mass-delete guard, the convergence check, and echo handling.
- **Exit criteria:** on test profiles (never the developer's real ones), for a LibreWolf–Brave pair and a Firefox–Firefox pair, a checklist passes. The run is recorded in the PR.
  - The first sync waits for approval, and `approve` with a stale plan id is refused.
  - Edits on each side sync.
  - Each worked example in Section 4 gives the tree shown.
  - Deleting 30 bookmarks stops for approval.
  - An edit made while a batch applies is kept.
  - Closing a browser mid-batch and reopening converges with no duplicates.
  - `restore` brings back a deleted bookmark, and `restore --backup` brings back a whole profile.

**Phase 3c: options page.** Label, status, errors and approval notices.

**Phase 4: packaging.**

- **Work:** `make release` builds binaries for Linux, macOS and Windows and the two extension packages; `install` gains macOS and Windows locations.
- **Exit criteria:** a fresh install on each OS, following only the README, connects LibreWolf and Brave.

End-to-end tests with real browsers, driven by WebDriver or the browsers' remote protocols, are a later RFC. Until then, Phase 3b's checklist is the gate.

**Rollback.** Before Phase 3b nothing writes bookmarks, so there is nothing to roll back. After it, `mirrormark pause` or `unpair` stops syncing a pair at once, and `restore --backup` returns each profile to any backed-up state.

## Success criteria

- **Sync works:** a LibreWolf profile and a Brave profile with a few hundred bookmarks each, including the user's real folders, hold the same bookmarks after the first approved sync. They stay the same after edits on either side, within 5 seconds of the last edit while both are open, as reported by `mirrormark status`.
- **Conflicts are merged:** each worked example in Section 4 produces the tree shown, in both profiles.
- **Nothing is lost:** every bookmark present in either live tree before a sync is present in both live trees afterwards, compared by URL and title (ignoring a suffix mirrormark added) after projecting onto each family. The one exception is a bookmark one side deleted and the other left unchanged, which is in neither tree and is in the journal. Backups do not count. The Phase 1 property tests and the Phase 3b checklist both check this.
- **Echoes don't loop:** after a sync, the next tree from each side produces zero ops; the convergence check never fires in the Phase 3b checklist.
- **Guards fire:** the first sync and a 30-bookmark deletion wait for approval, and a stale `approve` is refused.
- **Many browsers, one host:** three profiles connected at once all appear in `mirrormark profiles`, served by one daemon process.

## Security, privacy, operational impact

**Security.**

- No network listener. The daemon accepts connections only on a Unix socket in a directory with mode `0700`.
- Only extensions listed in the host manifests can start the shim; the browser enforces this [R1] [R2].
- Same-user processes can use the socket; see the last item under Tradeoffs. The daemon validates every message, executes nothing it receives, and applies the mass-delete guard to every source.
- The extension asks for no host permissions and reads no web pages.
- Dependencies: Go's standard library where possible. Each third-party module needs a reason in its commit.

**Privacy.** Bases, backups and the journal hold the user's bookmarks in the state directory, mode `0600`, uncompressed or gzip-compressed but not encrypted. Nothing leaves the computer. mirrormark collects no telemetry. `mirrormark uninstall --purge` deletes the state directory.

**Operations.**

- **Two installs.** Users install the host and the extension, and run `mirrormark install` again after installing a new browser.
- **Disk.** Backups take up to about 50 MB per pair (Section 6).
- **Logs.** Bug reports need the host log, which the daemon writes to the state directory and `mirrormark status` points to.
- **Signing.** Firefox release builds install only signed extensions permanently, so distribution needs addons.mozilla.org signing or a browser that allows unsigned extensions (Open question 8).

## Open questions

1. **Is "sync only while both are open" enough?** If the user rarely runs both browsers at once, the hub design should come forward. _Resolve in RFC_; ask the user.
2. **The three deliberate exceptions to "both versions survive, suffixed".** Proposed in Section 4: edit/delete keeps the edit without a suffix; a folder renamed on both sides keeps one folder titled `X (a) / Y (b)`; a title edit on one side and a URL edit on the other combine. Alternatives: suffix the restored bookmark in edit/delete so the user notices; keep the first-paired profile's folder title and log the other; treat split title/URL edits as edit/edit. _Resolve in RFC_; ask the user.
3. **Chromium account and local folders.** Proposed: sync the account set when it exists, otherwise the local set. Should the user choose, or should both be synced into separate places? _Resolve in implementation_, after testing a signed-in Brave or Chrome profile.
4. **URL normalisation.** Should `http`/`https` or a trailing slash make two URLs equal for matching? Proposed: no normalisation beyond what the browser already does. _Resolve in implementation._
5. **Changing a label.** Proposed: existing suffixes keep the old label. Should mirrormark offer to retitle them? _Resolve in implementation._
6. **Windows transport.** Go supports Unix sockets on Windows 10 and later, but we have not tested it, and named pipes need a third-party module. _Resolve in Phase 4._
7. **Default labels.** Does Brave detection via `navigator.brave` work in a service worker, and does `runtime.getBrowserInfo()` report "LibreWolf"? Unverified. Only the suggested default label depends on it. _Resolve in implementation._
8. **Distribution of the extension.** addons.mozilla.org listing or self-distributed signed XPI; Chrome Web Store or developer mode only. The Chrome Web Store gives a different id than the development `key`, so `install` must list both. _Defer_ until Phase 4.

Decided here, and recorded so they aren't reopened by accident: the transport is native messaging relayed to one daemon (Section 1 and Alternatives); groups of three or more and "ask which side wins" are later RFCs (Non-goals).

## References

All external claims were checked on 2026-10-14 unless marked unverified.

- **[R1]** MDN, "Native messaging": UTF-8 JSON with a 32-bit native-byte-order length; 1 MB from the app, 4 GB to it; `connectNative` keeps one process until the port closes; `allowed_extensions`; the host receives the manifest path and the add-on id; on close, `SIGTERM` then `SIGKILL` on Linux and macOS, and a killed job object on Windows unless children use `CREATE_BREAKAWAY_FROM_JOB`. https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_messaging. Manifest locations: https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_manifests
- **[R2]** Chrome, "Native messaging": 1 MB from the host, 64 MiB to it; one process per port; `allowed_origins` without wildcards; the first argument is the caller's origin; manifest and registry locations. https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging
- **[R3]** Chrome, "The extension service worker lifecycle": `connectNative()` keeps a service worker alive (Chrome 105); active WebSockets extend its lifetime (Chrome 116). https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle
- **[R4]** Mozilla bug 1770696, "MV3 non-persistent background pages vs native messaging host": the idle timer resets while native ports are open. https://bugzilla.mozilla.org/show_bug.cgi?id=1770696. Confirmed on Firefox 157 by a third-party test: https://github.com/pooriaarab/foxbridge/pull/22
- **[R5]** MDN, `bookmarks` and `BookmarkTreeNode`: node fields, events, the root cannot be modified, wait for each `create`/`move` when order matters. https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/bookmarks. Firefox's top-level GUIDs (`toolbar_____` and so on) are from Firefox's Places code as remembered, unverified against documentation.
- **[R6]** Chrome, `chrome.bookmarks`: `folderType` (`bookmarks-bar`, `other`, `mobile`, `managed`) and `syncing` since Chrome 134, with possibly several nodes of one folder type, distinguished by `syncing`; root id `"0"` (`ROOT_NODE_ID`); write limits removed; import events; `onChildrenReordered` fires for UI sorts, not for `move()`. https://developer.chrome.com/docs/extensions/reference/api/bookmarks
- **[R7]** Go, `encoding/binary.NativeEndian` (Go 1.21+). https://pkg.go.dev/encoding/binary
- **[R8]** LibreWolf native-messaging host locations (third-party): https://github.com/nix-community/home-manager/issues/6635, https://codeberg.org/librewolf/issues/issues/2308, https://pwasforfirefox.filips.si/help/faq/
- **[R9]** Brave native-messaging host location on Linux (third-party): https://community.brave.app/t/what-is-the-path-for-native-messaging-on-linux/616427, https://github.com/anthropics/claude-code/issues/14391
- **[R10]** floccus: MPL-2.0 bookmark sync across Firefox and Chromium browsers through WebDAV, Nextcloud, Git, Google Drive and others. https://github.com/floccusaddon/floccus
- **[R11]** MDN browser-compat-data, `webextensions/api/bookmarks.json`: Firefox `version_added: false` for `onImportBegan`, `onImportEnded` and `onChildrenReordered`. https://raw.githubusercontent.com/mdn/browser-compat-data/main/webextensions/api/bookmarks.json

## History

- 2026-10-14 — draft
