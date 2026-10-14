# Spike: installing mirrormark into LibreWolf on Linux

Run on 2026-10-15 in Docker, on the Firefox spike's image (`ubuntu:24.04`,
Xvfb, D-Bus, Developer Edition 159.0b2 for comparison) with LibreWolf's
official Linux tarball added. Nothing was installed on the host machine. The
scripts are in [`librewolf-spike/`](librewolf-spike/), described
[at the end](#the-scripts).

| Build | Download | sha256 |
| --- | --- | --- |
| LibreWolf 158.0-1 (Firefox 158.0, BuildID 20261013185131) | `https://dl.librewolf.net/librewolf/158.0-1/librewolf-158.0-1-linux-x86_64-package.tar.xz` | `34bb7296feec0c6823abdfaf4fd50e47e9eada9cb4a5ed16e111a951dc9eb16c` |

The checksum is the release's own `.sha256sum` file; the release is listed at
`https://codeberg.org/librewolf/bsys6/releases/tag/158.0-1`.

## Result

mirrormark's Developer Edition route works in LibreWolf unchanged: the same
XPI, the same three `user.js` prefs, the same host manifest in
`~/.mozilla/native-messaging-hosts/`, the same UUID in `prefs.js` and the
same bind URL. LibreWolf is a release build but is built **without
`MOZ_REQUIRE_SIGNING`**, so it honours `xpinstall.signatures.required`.

What stops today's `mirrormark install --browser firefox` (`exp9.sh`):

- it looks for profiles only under `~/.config/mozilla/firefox` and
  `~/.mozilla/firefox`;
- with `--user-data-dir ~/.config/librewolf/librewolf` it refuses every
  profile because `channel-prefs.js` says `release`;
- `Channel()` would return `<LastPlatformDir>/firefox`, which does not exist;
  LibreWolf's binary is `<LastPlatformDir>/librewolf`.

Supporting LibreWolf is therefore a matter of recognising it, not of a new
install route.

## Findings

### Layout and channel (`exp1.sh`, `exp1b.sh`)

- `/opt/librewolf/librewolf` is the binary (ELF; `librewolf-bin` beside it).
  `application.ini`: `Name=LibreWolf`, `RemotingName=librewolf`,
  `Profile=librewolf`, `ID={ec8030f7-…}` (Firefox's app id).
- A first launch creates `~/.config/librewolf/librewolf/profiles.ini` and
  `installs.ini`, with profiles `default-release` (the one used) and
  `default` (never started, so it has no `compatibility.ini`).
- `$XDG_CONFIG_HOME` is honoured: `XDG_CONFIG_HOME=~/xdg` gives
  `~/xdg/librewolf/librewolf/profiles.ini`.
- The legacy root `~/.librewolf/` is used only when it already holds a
  `profiles.ini`; an empty `~/.librewolf` or `~/.librewolf/librewolf` does
  not count.
- `compatibility.ini`: `LastPlatformDir=/opt/librewolf`,
  `LastAppDir=/opt/librewolf/browser`.
- `/opt/librewolf/defaults/pref/channel-prefs.js`:
  `pref("app.update.channel", "release")`. So the channel cannot tell
  LibreWolf from release Firefox; `application.ini`'s `Name=LibreWolf` (or
  `librewolf.cfg` in the same directory) can.

### Unsigned XPI and prefs (`exp2.sh`, `exp3.sh`, `exp3b.sh`)

Over BiDi in chrome scope: `Services.appinfo.name` `LibreWolf`,
`AppConstants.MOZ_REQUIRE_SIGNING` **false**, `MOZ_UPDATE_CHANNEL`
`release`. None of the relevant prefs is locked
(`Services.prefs.prefIsLocked`).

Shipped defaults (`librewolf.cfg`, `policies.json` and the default branch):

| Pref | Default | Locked |
| --- | --- | --- |
| `xpinstall.signatures.required` | true | no |
| `extensions.autoDisableScopes` | 15 | no |
| `extensions.startupScanScopes` | 0 | no |
| `extensions.enabledScopes` | 5 (`librewolf.cfg`: profile + application) | no |

`policies.json` sets `ExtensionSettings."*"` to `installation_mode:
"allowed"` for extensions (it blocks only language packs) and installs
uBlock Origin; nothing in it or in `librewolf.cfg` touches the three prefs.

The real extension dropped into `<profile>/extensions/`, two starts each,
state from `extensions.json` (no BiDi, see Gotchas):

| `user.js` | Profile | Each start |
| --- | --- | --- |
| signatures false, ADS 14, scan 1 | fresh | `active: true, userDisabled: false, signedState: 0, location: app-profile` |
| the same | started once before | the same |
| signatures false, ADS 15, scan 1 | fresh, and started once | `active: false, userDisabled: true, seen: false` |
| signatures line left out, ADS 14 | started once | not installed (no entry) |
| signatures true, ADS 14 | started once | not installed (no entry) |
| signatures false, ADS 14, **no scan line** | started once | `active: true` (`exp3b.sh`) |

So 14 suffices, 15 leaves it disabled, and the signatures pref is what lets
it in. Unlike Developer Edition, a profile that has run before picked up the
file without `startupScanScopes=1`; keeping the line costs nothing.

### Uninstall (`exp10.sh`)

Removing the XPI and turning the two lines into `true` / `15`, then one
restart: the add-on is gone from `extensions.json`, and `prefs.js` holds no
user value for either pref (LibreWolf drops values equal to the default).
The defaults are Firefox's, so `ResetPrefs` is right for LibreWolf too.
`startupScanScopes` stays 1, as in Firefox.

### Native messaging (`exp4.sh`, `exp7.sh`, `exp8.sh`)

- LibreWolf searches, in order, `~/.librewolf/native-messaging-hosts`,
  `/usr/lib/librewolf/native-messaging-hosts`,
  `~/.mozilla/native-messaging-hosts`, `/usr/lib/mozilla/native-messaging-hosts`
  (`XREUserNativeManifests`, `XRESysNativeManifests`,
  `XREMozUserNativeManifests`, `XREMozSysNativeManifests` in
  `NativeManifests.sys.mjs`). The first manifest found wins.
- With the XDG profile root, a manifest in `~/.mozilla/…` or
  `~/.librewolf/…` started the host; `~/.config/librewolf/…`,
  `~/.config/librewolf/librewolf/…` and `~/.config/mozilla/…` did not.
- Arguments: the manifest's path and the extension id, as in Firefox.
- An open port keeps the event page alive: a silent host's pings at 45, 100
  and 160 s were all answered.

### Binding (`exp5.sh`, under Xvfb and `dbus-run-session`)

- `extensions.webextensions.uuids` in `prefs.js`, as in Firefox; it appeared
  1.6–2.2 s after starting the profile with the XPI in place.
- Into a running instance:
  `librewolf --profile <dir> about:blank moz-extension://<uuid>/bind.html#db-alpha`
  exited 0, the running instance opened the page and the host got
  `hello` with `db: "db-alpha"`; the instance kept running.
- Cold: the same command with LibreWolf not running bound `db-beta`.
- D-Bus remoting name: `io.gitlab.librewolf.<base64 of the profile path>`,
  not `org.mozilla.firefox.…`. Nothing in mirrormark depends on the name.
- The extension's label is `firefox/158.0`: `resistFingerprinting` leaves
  the user agent as `… Firefox/158.0`.

### Bookmarks (`exp6.sh`)

The Firefox spike's probe, run in LibreWolf and Developer Edition in the
same container, with ids and times masked: identical roots
(`root________`, `menu________`, `toolbar_____`, `unfiled_____`,
`mobile______`), 12-character GUIDs, separators, move indexes, clamping and
errors. The only differences: LibreWolf has **no default bookmarks**
(`policies.json` `NoDefaultBookmarks`), so the menu holds 0 items instead of
1, and the probe's events arrived in a different order with one extra
`onChanged {title: "T"}` captured before the timeout.

### End-to-end testing (`exp2.sh`, `exp8.sh`)

`--remote-debugging-port` WebDriver BiDi works although `librewolf.cfg`
sets `devtools.debugger.remote-enabled` false (that is the old devtools
server). With `MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1` and
`--remote-allow-system-access`, chrome scope reached `Services`,
`AppConstants` and `PlacesUtils.bookmarks.insert`; content scope on
`moz-extension://<uuid>/bind.html` reached `chrome.storage` and
`chrome.bookmarks`. The e2e client in `e2e/bidi.go` should work as is.

### Privacy defaults (`exp5.sh`, `exp8.sh`)

- `privacy.sanitize.sanitizeOnShutdown` is true;
  `privacy.sanitize.pending` lists `cache` and `cookiesAndStorage` for
  shutdown. A persistent cookie present before a clean quit was gone after
  it, but the binding in `storage.local` survived: after a clean quit (SIGTERM,
  and `Services.startup.quit`) and restart the extension said `hello` with
  the same `db`, twice in a row.
- `privacy.resistFingerprinting` true: no effect seen beyond the user agent.
- `extensions.webextensions.restrictedDomains` is `""` (fewer restrictions
  than Firefox, not more).

## Gotchas

- **BiDi changes the prefs under test.** Starting with
  `--remote-debugging-port` applies the remote agent's recommended prefs,
  which set `extensions.autoDisableScopes` to 0 for that run. Install checks
  must read `extensions.json` from runs without BiDi.
- A first start of a fresh profile can take more than 10 s in this container
  before the event page connects; 15 s was reliable.
- LibreWolf creates a second profile, `default`, that never starts, so it has
  no `compatibility.ini`; install currently reports it as an error.

## Consequences for install and uninstall

- Profile root: `$XDG_CONFIG_HOME/librewolf/librewolf` (default
  `~/.config/librewolf/librewolf`) if it holds `profiles.ini`, else
  `~/.librewolf`. Same listing code as Firefox.
- Accept a profile when its `<LastPlatformDir>/application.ini` says
  `Name=LibreWolf` (channel `release` is then fine), and run
  `<LastPlatformDir>/librewolf`, not `firefox`.
- Files: unchanged. Same XPI, same three `user.js` lines, host manifest in
  `~/.mozilla/native-messaging-hosts/` (read by LibreWolf as a fallback;
  `~/.librewolf/native-messaging-hosts/` wins if the user has one there).
- Uninstall: unchanged. LibreWolf's defaults are `true` / 15, so resetting
  to them is right.
- Bind: unchanged apart from the binary.
- The safety note shown at install applies as for Developer Edition.

## Not verified

- LibreWolf's Debian/Ubuntu repository, Flatpak, AppImage, AUR and other
  packages; only the tarball in `/opt/librewolf` was run. Their install
  directory, and so `LastPlatformDir`, will differ.
- Whether a package that sets `/usr/lib/librewolf/native-messaging-hosts`
  or ships its own manifests changes which manifest wins.
- Older LibreWolf releases, and whether a later one turns on
  `MOZ_REQUIRE_SIGNING` or locks any of the three prefs.
- A user who changed LibreWolf's sanitize settings to clear more (for
  example site data exceptions, or "delete everything" in the UI); only the
  defaults were run.
- `mirrormark` as the host; the spike used the Firefox spike's logging host
  with mirrormark's real extension.
- A real desktop session.

## The scripts

They expect three mounts in the image built from `librewolf-spike/Dockerfile`
(`spike-librewolf`, built `FROM spike-firefox:latest`): this directory at
`/spike`, `firefox-spike/` at `/ff` (for `host.py`, the probe `ext/` and the
`bidi/` client) and the repository's `extension/` at `/real`. For example,
from the repository root:

```sh
S=docs/rfcs/0001_local-bookmark-sync-host.d
docker run --rm -v "$PWD/$S/librewolf-spike:/spike:ro" -v "$PWD/$S/firefox-spike:/ff:ro" \
  -v "$PWD/extension:/real:ro" spike-librewolf bash /spike/exp3.sh
```

`exp5.sh` runs under `dbus-run-session --`; `exp9.sh` also needs
`-v "$PWD/build:/mm:ro"` after `bin/dev run make build`.

| File | What it does |
| --- | --- |
| `Dockerfile` | LibreWolf 158.0-1 in `/opt/librewolf`, on the Firefox spike's image |
| `lib.sh` | Helpers: the real XPI zipped as `XPI()` does, the probe XPI, the BiDi client, profile lookup, timed runs, add-on state |
| `user-mm.js` | mirrormark's three `user.js` lines |
| `exp1.sh`, `exp1b.sh` | Profile root, XDG, legacy root, `compatibility.ini` |
| `exp2.sh` | Pref defaults and locks, `MOZ_REQUIRE_SIGNING`, the drop-in over BiDi |
| `exp3.sh`, `exp3b.sh` | The drop-in matrix (ADS 14/15, signatures pref, fresh/existing), and without `startupScanScopes` |
| `exp4.sh`, `exp4b.sh` | Which host-manifest directory is read, host arguments |
| `exp5.sh` | Binding cold and by remoting, D-Bus name, binding across clean restarts |
| `exp6.sh` | Bookmark probe in LibreWolf and Developer Edition, diffed |
| `exp7.sh` | Event-page keep-alive with a silent host |
| `exp8.sh` | BiDi chrome and content scope, manifest search path, sanitize control |
| `exp9.sh` | Today's `mirrormark install --browser firefox` against LibreWolf |
| `exp10.sh` | Uninstall: remove the XPI, reset the prefs, restart |
