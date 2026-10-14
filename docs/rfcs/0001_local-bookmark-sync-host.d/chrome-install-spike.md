# Spike: installing a local extension into Google Chrome on Linux

Run on 2026-10-14 in Docker. Google Chrome 155.0.8059.79 (from Google's
`.deb` in `ubuntu:24.04`); Chrome for Testing (CfT) 155.0.8059.39; Go from
`golang:1.24`. Nothing was installed on the host machine.

## Result

The route that works for branded Chrome is the **external extensions JSON**:
`/opt/google/chrome/extensions/<id>.json` (or
`/usr/share/google-chrome/extensions/<id>.json`) containing
`{"external_crx": "/abs/path.crx", "external_version": "<exact version>"}`.

- Needs root; no per-user location worked.
- Applies to every profile of every OS user; Chrome cannot target one profile.
- The extension starts **enabled**, with no prompt (`location 2`,
  `disable_reasons []`, install type `sideload`), headless or under Xvfb.
- Existing profiles get it on their next start, including the real default
  data dir.

| Mechanism | Branded Chrome 155 | CfT 155 | Root | Notes |
| --- | --- | --- | --- | --- |
| External JSON (`/opt/google/chrome/extensions`, `/usr/share/google-chrome/extensions`) | works, enabled | own dirs: `/usr/share/chromium/extensions`, `<exe dir>/extensions` | yes | all profiles |
| Policy `ExtensionInstallForcelist` / `ExtensionSettings` with a `file://` update.xml and CRX | works (location 7) | works via `/etc/opt/chrome_for_testing/policies/managed` | yes | all profiles; user can't remove; "Managed by your organization" |
| `--load-extension` | ignored ("not allowed in Google Chrome") | works (location 8) | no | only for that launch |
| Hand-editing `Preferences` | not tried | | | fragile (MACs; Chrome must be closed) |

## Native messaging

- `chrome --headless=new --pack-extension=<dir> --pack-extension-key=<pem>`
  makes a CRX3; a ~100-line stdlib Go CRX3 writer also works.
- The host is started with only the origin `chrome-extension://<id>/` as an
  argument: **nothing identifies the profile**.
- User-level host manifests are read from
  `<user-data-dir>/NativeMessagingHosts/` (so `~/.config/google-chrome/...`
  only for the default data dir). The system-wide location for branded Chrome
  is `/etc/opt/chrome/native-messaging-hosts/`; CfT reads
  `/etc/opt/chrome_for_testing/native-messaging-hosts/`.
- The host was reached 1–2 s after launch; replies arrive in about 1 ms.
- An open native port keeps the MV3 service worker alive (pings continued
  past 70 s; without a host the worker was gone by 75 s).
- The host sees EOF on stdin when Chrome or the port closes.

## Gotchas

1. Every working route for branded Chrome needs root and covers all profiles.
2. Chrome reads the external JSON only at startup; a restart is needed.
3. `external_version` must exactly match the CRX's manifest version.
4. A user's **Remove** is permanent for that profile
   (`extensions.external_uninstalls`); a disable also sticks.
5. Deleting the JSON uninstalls the extension at the next start.
6. On a plain restart the MV3 service worker does not start unless the
   extension listens for an event; `chrome.runtime.onStartup` fixes it.
7. Since Chrome 136, CDP can't attach to the default data dir.
8. In Docker, Chrome needs `--no-sandbox`; close it with CDP
   `Browser.close` so `Preferences` is written.

## Consequences for the install command

- `sudo mirrormark install` copies the CRX and the binary to a world-readable
  place, writes the external JSON with the exact version and the system-wide
  host manifest, and asks the user to restart Chrome.
- The extension is installed in every profile, so it stays dormant until a
  profile is bound to a database file; it must not `uninstallSelf`.
- The extension connects from top-level service-worker code and registers
  `onStartup` and `onInstalled`.
- `--load-extension` with CfT is fine for quick tests of the extension, but
  not for testing the install path.

## Binding by URL (run while planning)

Same image, Chrome 155.0.8059.79 under Xvfb, one data directory with
`Default` and `Profile 1`, extension installed through the external JSON.
A `bind.html` page stored `location.hash` in `storage.local` and sent it to
the host.

- `google-chrome --user-data-dir=U --profile-directory=X
  chrome-extension://<id>/bind.html#<db>` opens the page **in profile X**
  and the page runs, both when Chrome is running ("Opening in existing
  browser session") and when it is not.
- It fails when X opens for the first time after the external JSON was
  added: the tab loads before the extension is installed in X. Opening the
  URL again 4 s later works. This held with Chrome running in another
  profile and with Chrome cold.
- So `install` must confirm a binding (through the host) and retry.

## DevTools over a pipe

`google-chrome --remote-debugging-pipe` (CDP on fds 3 and 4, messages
ended by NUL) works in branded Chrome 155 under Xvfb with a non-default
data directory. A second `google-chrome --user-data-dir=<same>` invocation
with a URL still forwards to the running instance, and `Browser.close`
exits cleanly. A Go e2e driver therefore needs no WebSocket client.
