# Spike: running mirrormark in Firefox on Linux

Run on 2026-10-15 in Docker (`ubuntu:24.04`). Firefox Developer Edition
159.0b2 and Firefox 158.0 from Mozilla's tarballs; Chrome for Testing (CfT)
155.0.8059.79 for the one Chrome check. Nothing was installed on the host
machine. The scripts are in [`firefox-spike/`](firefox-spike/), described
[at the end](#the-scripts).

| Build | Download | sha256 |
| --- | --- | --- |
| Developer Edition 159.0b2 | `https://download-installer.cdn.mozilla.net/pub/devedition/releases/159.0b2/linux-x86_64/en-US/firefox-159.0b2.tar.xz` | `710dfa80e9edcad6c31f5aa0b3dffb4164b14a60be09316621de3894d8944b40` |
| Firefox 158.0 (release) | `https://download-installer.cdn.mozilla.net/pub/firefox/releases/158.0/linux-x86_64/en-US/firefox-158.0.tar.xz` | `9549b054ec4cf85f50f6a19d8c6fb79580312e71ebc4ec2cb217ec9af09dc12a` |

## Result

mirrormark's extension runs in Firefox Developer Edition with small changes,
installed per profile and without root. Release Firefox refuses it until it
is signed by Mozilla.

- **Install, per profile, no root.** Copy the XPI to
  `<profile>/extensions/mirrormark@chriscz.github.io.xpi` and append to
  `<profile>/user.js`:

  ```js
  user_pref("xpinstall.signatures.required", false);
  user_pref("extensions.autoDisableScopes", 0);
  user_pref("extensions.startupScanScopes", 1);
  ```

  On the next start Developer Edition installs the add-on **enabled, with no
  prompt**. One restart is needed when Firefox was running in that profile
  while the files were dropped. `extensions.startupScanScopes=1` is what
  makes a profile that has run before notice the new file; a fresh profile
  needs only the first two.
- **Safety cost.** With `xpinstall.signatures.required=false` that profile
  accepts *any* unsigned add-on, not only mirrormark's, and with
  `autoDisableScopes=0` an add-on dropped into the profile directory starts
  enabled without asking. Only a program that can already write the profile
  directory can use this, but the user must be told.
- **Release Firefox 158.0 rejects the unsigned XPI** even with the prefs;
  `xpinstall.signatures.required` is ignored on release builds. It needs a
  Mozilla-signed XPI.
- **`policies.json`** (`ExtensionSettings` with a `file://` `install_url`)
  also works, but it lives in Firefox's installation directory, applies to
  every profile of every user, and marks Firefox as managed. Rejected.

**`autoDisableScopes` 14 instead of 0** (checked later the same day, same
image). `autoDisableScopes` is a bitmask of scopes whose sideloaded add-ons
start disabled (profile 1, user 2, application 4, system 8; default 15), so 0
also enables unsigned add-ons dropped into `~/.mozilla/extensions` or a
system directory. 14 clears only the profile bit. The drop-in was rerun as in
`exp1c.sh`, with the three lines appended to `user.js` and two starts of 15 s
each, reading the add-on from `extensions.json`:

| `autoDisableScopes` | Profile | Each start |
| --- | --- | --- |
| 14 | fresh | `active: true, userDisabled: false, location: app-profile`; the background connected to the host (`onInstalled:install`, then `onStartup`) |
| 14 | started once without the add-on | the same |
| 0 | fresh, and started once | the same |
| 15 | fresh, and started once | `active: false, userDisabled: true, seen: false`: installed disabled, awaiting the user's consent |

So 14 is enough, and mirrormark uses it. An unsigned add-on placed in the user
scope (`~/.mozilla/extensions/{ec8030f7-…}/` and `~/.config/mozilla/extensions/{ec8030f7-…}/`)
was not picked up at all in this build, with 14 or 0, so the gain over 0 was
not observed directly; it rests on the bitmask.

## One manifest for both browsers

Adding these keys to the existing `manifest.json` is enough:

```json
"background": { "service_worker": "background.js", "scripts": ["background.js"] },
"browser_specific_settings": { "gecko": { "id": "mirrormark@chriscz.github.io", "strict_min_version": "128.0" } }
```

Firefox uses `scripts` (an MV3 event page) and ignores `service_worker`.
Chrome uses `service_worker` and ignores the other two; CfT 155 loaded it
with **no install warnings, manifest errors or runtime errors**
(`expchrome2.sh`, through `chrome.developerPrivate`). Branded Chrome with
the CRX route was not re-run with this manifest.

## The extension on Firefox

- **`background.js` crashes at load**: `bookmarks.onChildrenReordered`,
  `onImportBegan` and `onImportEnded` do not exist in Firefox, and calling
  `addListener` on `undefined` throws. Skipping events that are missing is
  enough; `onCreated`, `onRemoved`, `onChanged` and `onMoved` exist.
- `chrome.*` works in Firefox MV3 and returns promises, so no `browser.*`
  shim is needed.
- The browser label comes from `navigator.userAgent`; Firefox's has
  `Firefox/159.0`, not `Chrome/`. (`runtime.getBrowserInfo()` also works,
  Firefox only.)
- The event page started with the profile on each start and connected to
  the host (`exp1c.sh`).
- An open native port **keeps the event page alive**: a silent host's
  pings at 45, 100 and 160 s were all answered (`exp3.sh`).

## Native messaging

- Firefox reads the per-user host manifest
  `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json`,
  with `"allowed_extensions": ["mirrormark@chriscz.github.io"]` instead of
  `allowed_origins`. No root. It does **not** read
  `~/.config/mozilla/native-messaging-hosts/`, even when the profiles live
  under `~/.config/mozilla/firefox/` (`exp3b.sh`).
- Firefox starts the host with **two arguments**: the host manifest's path
  and the extension's id. Nothing identifies the profile, as in Chrome.
- Framing is the same (native-endian length, UTF-8 JSON).
- The host sees EOF on stdin when Firefox or the port closes.

## Binding a profile

- The extension's pages live at `moz-extension://<uuid>/`, where the UUID is
  chosen per profile at install time and stored in `<profile>/prefs.js` as
  `extensions.webextensions.uuids` (a JSON string mapping add-on id to
  UUID). It appears there a few seconds after the start that installs the
  add-on, while Firefox runs (`exp7.sh` read it 4 s after launch).
- **Pinning the UUID through `user.js` is unsafe:** setting
  `extensions.webextensions.uuids` replaces the whole map, so other add-ons
  get new UUIDs and lose their storage (`exp4b.sh`). Read it instead.
- `<firefox> --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>`
  opens the page in that profile, both cold and when Firefox already runs
  in it (forwarded over D-Bus remoting). The page ran and stored the
  binding both ways (`exp4.sh`).
  - Remoting needs a D-Bus session. It does not work with `--headless`: the
    second invocation does not reach the running headless instance
    (`exp4c.sh`).
  - `bind.js` calls `window.close()`. When the bind tab is the only tab of
    the only window, that **quits Firefox**. Opening `about:blank` alongside
    avoids it.
- The Firefox binary to run is `<LastPlatformDir>/firefox`, from the
  profile's `compatibility.ini`; the channel is in
  `<LastPlatformDir>/defaults/pref/channel-prefs.js`
  (`pref("app.update.channel", "aurora")` for Developer Edition, `release`
  for release).

## Bookmarks

From `exp5.sh` (Firefox) and `expchrome.sh` (CfT), running the same probe:

- **Roots** have fixed ids and no `folderType`: the tree root
  `root________`, with children `menu________` (Bookmarks Menu),
  `toolbar_____`, `unfiled_____` (Other Bookmarks) and `mobile______`. Their
  titles are what Firefox shows. No `syncing` field. Mapping:
  `toolbar_____` → `toolbar`, `unfiled_____` → `other`, `mobile______` →
  `mobile`, and `menu________` → `menu`, which Chrome lacks.
- **Ids** are 12-character GUIDs, not small integers.
- **Separators** exist only in Firefox: `type: "separator"`, `url: "data:"`,
  `title: ""`. Chrome has none.
- **Same-parent move indexes differ.** Moving a child down within its parent
  to index *i* puts it at final position *i* in Firefox; Chrome counts *i*
  before removing it from its old place, so it lands at *i − 1*.
- An index past the end is **clamped** by Firefox. (Chrome rejects it,
  which is why the extension already clamps.)
- `bookmarks.remove` on a folder that is not empty **throws** in both.

The probe also created bookmarks with `data:`, `place:`, `javascript:`,
`about:`, `file:` and `chrome://` URLs and a non-canonical URL; those results
were not kept, so which URLs Firefox refuses or rewrites is **not verified**.

## Profiles

- The profiles root is `~/.config/mozilla/firefox/` for a new install
  (Firefox 147 and later) or `~/.mozilla/firefox/` when that already exists
  (`exp6.sh`). Both hold `profiles.ini` (`[ProfileN]` with `Name`,
  `IsRelative`, `Path`) and `installs.ini`. A fresh Developer Edition
  creates a profile named `dev-edition-default`.
- Profiles made with Firefox's **new profile switcher** are listed only in
  `<root>/Profile Groups/<StoreID>.sqlite` (`exp6b.sh`). Go's standard
  library cannot read SQLite. Every profile directory, however it was made,
  holds a `prefs.js`, so scanning `<root>/*/prefs.js` finds them; the name
  is the directory name after its first `.`.

## End-to-end testing

- A ~120-line standard-library Go WebSocket client speaks WebDriver BiDi to
  Firefox started with `--remote-debugging-port N` (`bidi/main.go`):
  `session.new`, `browsingContext.getTree`, `browsingContext.navigate` and
  `script.evaluate` with `awaitPromise`.
- **Content scope:** navigating a tab to `moz-extension://<uuid>/bind.html`
  and evaluating there gives the page `chrome.bookmarks` and
  `chrome.storage`, as an extension page in Chrome does (`exp7.sh`).
- **Chrome scope** (`"moz:scope": "chrome"`, which needs
  `MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1` and `--remote-allow-system-access`)
  reaches `PlacesUtils.bookmarks`, so a test can edit bookmarks the way the
  UI does; the extension saw those edits as events (`exp7.sh`).
- Headless works for everything except binding into a running instance,
  which needs Xvfb and a D-Bus session (`dbus-run-session`).

## Verified and not verified

Verified on Developer Edition 159.0b2 in Docker: the per-profile install
and its prefs; release 158.0 refusing it; the shared manifest (Firefox and
CfT 155); the missing events; the per-user host manifest and its arguments;
the port keeping the event page alive for 160 s; the UUID in `prefs.js`;
binding cold and by remoting; the bookmark roots, ids, separators, move
indexes, clamping and folder removal; profile listing; the BiDi client.

Not verified:

- Nightly, ESR, unbranded builds, LibreWolf and distribution packages
  (Snap, Flatpak). Only the `aurora` channel was run.
- Firefox older than 159. `strict_min_version` 128.0 is a guess.
- Which URLs Firefox refuses or rewrites (see Bookmarks).
- The reconnect alarm in a Firefox event page, and an event page with no
  port open being woken by a bookmark event.
- BiDi (`--remote-debugging-port`) and D-Bus remoting in the same
  instance, and `--remote-debugging-port 0`.
- Whether `$XDG_CONFIG_HOME` moves Firefox's profiles root.
- Undoing the prefs on uninstall, and a real desktop session (the spike ran
  only in Docker).

## Consequences

- One `manifest.json` for both browsers, one `background.js` that skips
  missing events and maps Firefox's roots by id.
- `install` for Firefox needs no `sudo`: the XPI and `user.js` lines go in
  each bound profile, the host manifest in `~/.mozilla/`.
- Only Developer Edition can be supported until the XPI is signed.
- Binding reuses `bind.html`, with the UUID read from `prefs.js`, the binary
  from `compatibility.ini`, and `about:blank` opened alongside.
- The extension must hide separators from the host and count indexes
  without them.
- The e2e tests can drive Firefox with a small BiDi client, under Xvfb and
  `dbus-run-session`.

## The scripts

Copied as run. They expect to be mounted at `/spike` in the image built from
`firefox-spike/Dockerfile` (which copies Go from the `mirrormark-dev`
image), as user `u`, for example
`docker run --rm -v "$PWD/firefox-spike:/spike" spike-firefox bash /spike/exp5.sh`.

| File | What it does |
| --- | --- |
| `Dockerfile` | Ubuntu 24.04 with Developer Edition 159.0b2 in `/opt/devedition`, release 158.0 in `/opt/release`, Xvfb, D-Bus |
| `Dockerfile.cft` | Adds CfT 155.0.8059.79 to the Chrome spike's image (`spike-chrome`, not kept) |
| `lib.sh`, `mkxpi.sh` | Helpers: zip `ext/` as an XPI, find a profile directory, run Firefox for N seconds, print the add-on's state |
| `ext/` | The probe extension: reports which events exist and runs bookmark commands from the host |
| `host.py` | Native host that logs its arguments, environment and messages and sends commands from `~/cmds.json` |
| `user-basic.js`, `user-scan.js` | The `user.js` prefs, without and with `startupScanScopes` |
| `run1.sh`, `exp1*.sh` | Install by file drop (fresh, previously run, while running) and by `policies.json` (`exp1p.sh`) |
| `exp2.sh` | mirrormark's real extension on Firefox, with and without `background.scripts`; expects a copy of `extension/` at `/spike/real` |
| `exp3.sh`, `exp3b.sh` | Event-page keep-alive; which host-manifest directory Firefox reads |
| `exp4.sh`, `exp4b.sh`, `exp4c.sh` | Binding by URL, cold and by remoting; why not to pin UUIDs; headless remoting |
| `exp5.sh` | Bookmarks API probe (roots, separators, move indexes, URLs) |
| `exp6.sh`, `exp6b.sh` | Profile roots and the new profile switcher |
| `exp7.sh` | BiDi in chrome and content scope |
| `expchrome.sh`, `expchrome2.sh` | The same probe and the shared manifest in CfT |
| `bidi/` | The BiDi (and CDP) client; its own module, so the repository's build skips it |
