# Spike: where browsers record an extension's state in a profile

Run on 2026-10-15 in Docker, in the earlier spikes' images: Google Chrome
155.0.8059.79 (`spike-chrome`), Brave 1.97.57 on Chromium 155.0.8059.80
(`spike-brave`), Firefox Developer Edition 159.0b2 (`spike-firefox`) and
LibreWolf 158.0-1 (`spike-librewolf`). The extension is the repo's: the CRX
packed by `mirrormark unpack` with its per-install key, and the XPI zipped
from `extension/` as `firefox.XPI()` does. Nothing was installed on the
host machine.

The question: can `install` tell, from a profile's own files and before it
opens anything, whether mirrormark is installed and enabled there, and say
why not if it is not?

Scripts: [state-spike/](state-spike/), run with
`MM=<mirrormark binary> state-spike/run.sh IMAGE EXP [VAR=value...]`:

| Run | Command |
| --- | --- |
| Brave | `run.sh spike-brave chromium` |
| Chrome | `run.sh spike-chrome chromium BRAVE=google-chrome COMM=chrome EXTDIR=/usr/share/google-chrome/extensions` |
| LibreWolf | `run.sh spike-librewolf gecko` |
| Developer Edition | `run.sh spike-firefox gecko B=/opt/devedition/firefox` |

`chromium.sh` reuses the Brave spike's `lib.sh` and `cdp.py`, and does the
user's Disable, Enable and Remove with `chrome.management` over the DevTools
pipe (`mgmt.py`). `gecko.sh` does them with `AddonManager` in the chrome
context over Marionette (`marionette.py`). `st.sh` prints what a Chromium
profile's two preference files say.

## Result

Yes, in all four browsers. Each profile keeps the extension's state in one
JSON file that `install` can read without the browser's help. Gecko writes
its file within about a second. Chromium writes its file up to about 17
seconds after the change, so a running Chromium browser's file can be stale.

## Chrome and Brave: `<profile>/Preferences`

The two browsers gave the same values. The state is in the profile's
`Preferences`, at `extensions.settings.<id>`. `Secure Preferences` held no
entry for the extension in any run. Chromium 155 writes no `state` field;
`disable_reasons` alone says whether the extension is off.

| Situation | `extensions.settings.<id>` | `extensions.external_uninstalls` |
| --- | --- | --- |
| Profile has run, JSON not read yet | no entry | no `<id>` |
| Installed and enabled | 25–26 keys, `location` 2, `disable_reasons` `[]` or absent, `manifest.version` `0.1.0`, `path` `<id>/0.1.0_0`, `from_webstore` false | no `<id>` |
| Disabled by the user | `disable_reasons` `[1]` (user action), all else as enabled | no `<id>` |
| Enabled again | `disable_reasons` key removed | no `<id>` |
| Removed by the user | `{}` (empty object) | `["<id>"]` |
| Removed, then started again | `{}` | `["<id>"]`; it does not come back |
| Profile created, never started | no `Preferences` file | |

`chrome.management` called the user's Remove an uninstall
(`uninstallSelf`, as the extension's own Remove does). The extensions page's
Remove asks in a native dialog that the DevTools protocol cannot answer.

**Writes lag.** Chromium batches preference writes:

- A running browser never read a JSON file that appeared after it started.
  After 40 s and a clean close, `Default` still had no entry (Chrome and
  Brave, step 2).
- After a cold start, the install reached `Preferences` 10.3 s (Brave) and
  17.2 s (Chrome) after launch (step 3). The extension itself was loaded
  sooner: an earlier Brave run opened `bind.html` at about 5 s, and it ran
  while the file still said nothing.
- A profile opened in a browser that was already running got the extension
  then, and its file showed it after 10–11 s (step 4). A profile not opened
  since the restart has no entry.
- Disable reached the file after 4.5–5 s; enable after 8–10 s (step 5).
- Remove reached the file within 1 s (step 6).
- A clean close writes everything at once.

**What `bind.html` shows when the extension is not there.** In a profile
where the extension is disabled or removed, opening
`chrome-extension://<id>/bind.html#…` gives an error page,
`chrome-error://chromewebdata/`, with this text: "`<id>` is blocked / This page has been
blocked by Brave / ERR_BLOCKED_BY_CLIENT" ("…by Chrome" in Chrome). This is
the text the user saw on their desktop Brave. It means the extension was not
loaded in that profile. It is not a Brave block.

## Developer Edition and LibreWolf: `<profile>/extensions.json`

The two browsers gave the same values. The entry is in `addons[]` where
`id` is `mirrormark@chriscz.github.io`. The UUID is in `prefs.js`
(`extensions.webextensions.uuids`).

| Situation | `addons[]` entry | UUID in `prefs.js` | XPI |
| --- | --- | --- | --- |
| Profile has run, XPI not written | none | none | no |
| XPI and `user.js` written, not started since | none | none | yes |
| Installed and enabled | `active` true, `userDisabled` false, `appDisabled` false, `location` `app-profile`, `version` `0.1.0`, `signedState` 0 | yes | yes |
| Disabled by the user | `active` false, `userDisabled` true | yes | yes |
| Removed by the user | none | removed | **deleted** |
| Signatures required (`xpinstall.signatures.required` true) | `active` false, `appDisabled` true, `userDisabled` false | yes | yes |
| Profile never ran (`-CreateProfile` only) | no `extensions.json` | no `prefs.js` | no |

- The entry and the UUID appeared 1.0–1.6 s after the start that installed
  the add-on.
- Disable, enable and remove reached `extensions.json` within 0.5 s; the
  UUID left `prefs.js` within about 1 s of the remove.
- A user's Remove deletes the XPI and leaves no record, so Gecko does not
  remember it the way Chromium does. Writing the XPI again installs the
  add-on again at the next start.
- Marionette's chrome context needs `-remote-allow-system-access` in
  LibreWolf and `MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1` in Developer Edition.
  Only the spike needs this; `install` reads the files.

## Consequences for the install command

- `install` can check each profile before it opens anything:
  - Chromium: read `Preferences` `extensions.settings.<id>` and
    `extensions.external_uninstalls`. The extension is ready when the entry
    has a `path` and an empty or missing `disable_reasons`. It is removed
    when `<id>` is in `external_uninstalls` and the entry has no `path`; a
    copy loaded unpacked afterwards has a `path` again, and the list may
    keep the id. With no entry, it is not installed yet.
  - Gecko: read `extensions.json` and the UUID in `prefs.js`. The add-on is
    ready when the entry has `active` true and a UUID is recorded.
    `userDisabled` means the user turned it off. `appDisabled` means the
    browser refused it.
- "Not installed yet" in a running Chromium browser has three causes. The
  browser was running when the JSON was written; it reads that file only
  at start. The profile has not been opened since the restart. Or the
  browser has not written the file yet, which takes up to about 20 s. The
  message must name all three. The same lag applies after the user turns
  the extension back on.
- Once a profile's file says the extension is installed, the extension
  loads with the profile. Opening `bind.html` then cannot lose the race the
  earlier spikes saw, where the tab loaded before the extension was
  installed. One open, with one bounded wait for the extension to report
  in, is enough. `install` no longer needs to retry, and no longer needs to
  start a Gecko browser to learn the UUID.
- `manifest.version` (Chromium) and `version` (Gecko) let `install` tell an
  old build from the current one after an upgrade.

## Not verified

- A desktop Brave under firejail. The spike did not check whether firejail
  hides `/opt/brave.com/brave/extensions/` from Brave. That would also give
  "not installed" and "blocked by Brave".
- Whether Brave or Chrome keeps running in the background after its last
  window closes, on a desktop with that setting on. In that case "quit
  completely" also means its tray icon.
- Disable reasons other than 1 (user action), such as a blocklist or a
  failed requirement.
- Chromium older than 155, which may still write `state`.
