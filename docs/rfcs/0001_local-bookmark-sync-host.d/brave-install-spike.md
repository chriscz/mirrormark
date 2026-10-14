# Spike: installing a local extension into Brave on Linux

Run on 2026-10-15 in Docker. Brave 1.97.57 (Chromium 155.0.8059.80), from
Brave's apt pool in `ubuntu:24.04`:
`https://brave-browser-apt-release.s3.brave.com/pool/main/b/brave-browser/brave-browser_1.97.57_amd64.deb`,
SHA-256 `d79919a82aa869cf5121b641afa79958730c4bdaf36a0c51ae0f9c32f9c16bb9`
(from the repo's `dists/stable/main/binary-amd64/Packages`). The extension is
the repo's, packed by `mirrormark unpack` (CRX3, per-install key); the host is
a logging script. Scripts and image: [brave-spike/](brave-spike/), run with
`MM=<mirrormark binary> brave-spike/run.sh spike-brave expN`. Nothing was
installed on the host machine.

## Result

Brave installs the same way as Google Chrome. Only the external extensions
directory differs; the system host manifest is the one Chrome uses.

- External JSON in `/opt/brave.com/brave/extensions/<id>.json` (the
  executable's directory) or `/usr/share/chromium/extensions/<id>.json`, with
  the same `external_crx` and `external_version`.
- The extension starts **enabled**, with no prompt (`location 2`,
  `disable_reasons []`), in existing profiles, in a profile created later,
  and in the default data directory.
- Host manifest: `/etc/opt/chrome/native-messaging-hosts/`, Chrome's own
  directory. Brave reads no Brave-named directory.
- The extension id depends only on the key, so the same key gives the same
  id in Chrome and Brave, and one host manifest serves both.

| Mechanism | Brave 1.97.57 | Root | Evidence |
| --- | --- | --- | --- |
| External JSON, `/opt/brave.com/brave/extensions` | works, enabled | yes | exp2 |
| External JSON, `/usr/share/chromium/extensions` | works, enabled; Chromium reads it too | yes | exp2 |
| External JSON, `/usr/share/{brave,brave-browser,BraveSoftware,BraveSoftware/Brave-Browser}/extensions`, `/usr/share/google-chrome/extensions`, `/opt/google/chrome/extensions` | ignored | | exp2 |
| `--load-extension` | works (location 8), same id from the manifest `key` | no | exp5 |
| Policy | not tried; the binary names `/etc/brave/policies` | yes | `strings` |

Brave's chrome://extensions page shows a "Safety Check: Review 1 extension
that may be unsafe … Brave can't verify where this extension comes from"
card, with the extension still **On**. Branded Chrome 155 shows the same card
for the same install (exp6, both screenshots), so this is not Brave's own
block, but the user does see it in both browsers.

## Data directory, profiles, binary

- Binary `/usr/bin/brave-browser` (alternatives) and
  `/usr/bin/brave-browser-stable`, both to the wrapper
  `/opt/brave.com/brave/brave-browser`, which runs `/opt/brave.com/brave/brave`
  (process name `brave`). `brave-browser --version` prints
  `Brave Browser 155.1.97.57`.
- Default data dir `~/.config/BraveSoftware/Brave-Browser`;
  `XDG_CONFIG_HOME=/x` moves it to `/x/BraveSoftware/Brave-Browser`;
  `CHROME_CONFIG_HOME` has no effect (exp1).
- `Local State` has `profile.info_cache` like Chrome: `Default` is named
  "Personal", the next one "Person 1" in `Profile 1` (exp1).

## Native messaging

- System manifests are read only from `/etc/opt/chrome/native-messaging-hosts/`.
  `/etc/chromium/…`, `/etc/brave/…`, `/etc/opt/brave/…`,
  `/etc/opt/brave.com/…`, `/etc/BraveSoftware/…` and
  `/etc/opt/BraveSoftware/…` were not read, though the binary also names
  `/etc/chromium/native-messaging-hosts` (exp3). Chrome need not be installed.
- The per-user manifest is read from the **default** data dir,
  `~/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/`, even for a
  browser started with another `--user-data-dir`; that directory's own
  `NativeMessagingHosts/` is not read ("Can't find manifest for native
  messaging host", exp3b). This differs from what the Chrome spike found.
- The host is started with only `chrome-extension://<id>/` (exp3, exp4).
- The hello reports `browser: "chrome/155.0.0.0"`: the user agent is
  Chrome's, so the host cannot tell Brave from Chrome.
- An open port keeps the MV3 service worker alive: the worker was still
  listed after 75 s, and the host saw EOF only when Brave closed (exp4).

## Binding by URL

`brave-browser --user-data-dir=U --profile-directory=X
chrome-extension://<id>/bind.html#<db>` opens the page in profile X and
stores the binding there, cold and with Brave running in another profile
(exp4). The first-start race is the same as Chrome's: the first open in a
profile after the JSON appeared lost to the install, cold and running; a
second open 4 s later bound and the host had the hello within 4 s.
Mirrormark's three tries 4 s apart cover it.

**Brave reopens the last session at startup by default**; Chrome opens a new
tab (exp7, after both a clean close and SIGTERM). A bind.html tab from a
failed first try stays open, so at the next start it loads again and binds
the profile again. In exp4 it bound Default back to the first try's
database, `d1`, though `d3` had been bound since. Mirrormark opens the same
database id in every try, so this rebinds to the same database. Only an old
failed tab with an older id would be wrong.

## Bookmarks

`chrome.bookmarks.getTree()` matches Chrome: root `0`, `1` "Bookmarks bar"
(`folderType: bookmarks-bar`), `2` "Other bookmarks" (`other`), no mobile
folder while it is empty, `syncing: false` (exp4). The extension mapped them
to `toolbar` and `other` unchanged.

## DevTools over a pipe

`--remote-debugging-pipe` works under Xvfb with a non-default data dir
(`Browser.getVersion` gives `Chrome/155.0.8059.80`), and `Browser.close`
exits cleanly. A second `brave-browser --user-data-dir=<same>` with a URL
forwards to the running browser ("Opening in existing browser session").

## Uninstall

Deleting the JSON removed the extension from every profile at their next
start: no prefs entry, no `Extensions/<id>` directory, no
`external_uninstalls` entry (exp4, exp5).

## Gotchas

1. The external directory is not Chrome's; the host manifest directory is.
2. `/opt/brave.com/brave/extensions` sits inside the package's own tree;
   `/usr/share/chromium/extensions` also reaches Chromium. Prefer the first.
3. Session restore replays a failed bind.html tab at the next start (see
   above).
4. In Docker, killing every `brave` process (children too) marked the
   profile as crashed. Send SIGTERM to the browser process only, or use CDP
   `Browser.close`.

## Consequences for the install command

- A Brave target needs only one extra path: write the same external JSON to
  `/opt/brave.com/brave/extensions/<id>.json`. The CRX, the key, and the host
  manifest in `/etc/opt/chrome/native-messaging-hosts/` stay as they are.
  With one key both browsers get the same id, and one `allowed_origins`
  entry covers both.
- Profiles: read `~/.config/BraveSoftware/Brave-Browser/Local State` (or
  `$XDG_CONFIG_HOME/BraveSoftware/Brave-Browser`); the Chrome code applies
  with another directory name.
- Binding: run `brave-browser` with the same arguments and the same retries.
- Uninstall: delete the Brave JSON too. Keep the host manifest while either
  browser is still installed.

## Not verified

- Brave Beta and Nightly (other binary names and data dirs), Flatpak and Snap
  Brave.
- Brave Sync turned on: whether it changes `syncing` or the bookmark ids.
- Whether a Brave package upgrade or removal leaves files in
  `/opt/brave.com/brave/extensions` alone.
- The policy route and its "managed" banner.
- The real `mirrormark` host and its lock-file check (a logging host was
  used; the hello and the arguments it saw are what the check relies on).
- CDP with the default data dir. Chrome blocks this since 136; it was not
  tried in Brave.
