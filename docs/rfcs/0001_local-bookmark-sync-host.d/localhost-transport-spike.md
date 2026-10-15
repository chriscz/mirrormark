# Spike: the extension talking to a local server over a WebSocket

Run on 2026-10-15 in Docker, on the dev image (`mirrormark-dev`: Google
Chrome 155.0.8059.79, Brave 1.97.57 (Chromium 155), Firefox Developer
Edition 159.0b2, LibreWolf 158.0-1, Xvfb) plus zip, xwd, ImageMagick and
iproute2. Nothing was installed on the host machine. The scripts are in
[`localhost-transport-spike/`](localhost-transport-spike/), described
[at the end](#the-scripts).

The question: can mirrormark drop native messaging and have the extension
connect to a long-running `mirrormark serve` on `127.0.0.1` instead? The
firejail half of the original question (questions 5 to 9) was deferred until
the transport exists; nothing about firejail was run.

## Result

Yes, in all four browsers, with two changes to the extension and one rule
in the server.

- **`new WebSocket("ws://127.0.0.1:PORT/")` needs no `host_permissions`** in
  Chrome, Brave, Developer Edition or LibreWolf. No prompt, no warning.
- **Firefox and LibreWolf need a manifest CSP.** Gecko's default MV3
  extension CSP contains `upgrade-insecure-requests`, which rewrites
  `ws://127.0.0.1` to `wss://` (seen in `MOZ_LOG`), so the socket fails with
  close code 1015. With
  `"content_security_policy": {"extension_pages": "script-src 'self'"}` it
  connects. Chrome and Brave accept the same key, and their sockets connect
  with or without it.
- **`fetch("http://127.0.0.1:PORT/")` needs `host_permissions:
  ["http://127.0.0.1/*"]`** (or CORS headers from the server) in all four.
  `ws://127.0.0.1/*` alone does not allow it. A WebSocket-only design needs
  no host permission.
- **Keeping the background alive differs by engine.** In Chrome and Brave, a
  server message every 20 s kept the service worker running for the whole
  9-minute run. In Firefox and LibreWolf, socket traffic does not count: the
  event page is stopped 30 s after its last *extension* event, socket open
  or not. Calling an extension API on each server message
  (`runtime.getPlatformInfo()` or `storage.local.set()`) kept it alive to
  the end of each run, up to 8 minutes.
- **With no traffic, every browser stops the background 30 s after the last
  event**, and the browser closes the socket (the server sees close code
  1001). The worker's `onclose` does not run then, so nothing reconnects
  unless an alarm is already scheduled.
- **An alarm reconnects reliably.** After the server was killed and
  restarted 15 s later, a one-shot 30 s alarm set in `onclose` (what
  `background.js` does today) reconnected in all four browsers within 30 s,
  whether the worker was still alive or had been stopped. A periodic 1-minute
  alarm restarts a stopped worker every minute, which reconnects.
- **The server sees `Origin: chrome-extension://<id>`** (Chrome, Brave) or
  **`moz-extension://<uuid>`** (Firefox, LibreWolf; the UUID in the
  profile's `prefs.js`). **Web pages can connect too**, with their own
  origin: any page served from `127.0.0.1` in every browser, a page on a
  private address in Chrome, Brave and Developer Edition, and, after the
  user clicks *Allow* on Chrome's Local Network Access prompt, an HTTPS page
  on a public address. So the server must check `Origin` exactly and require
  the per-profile token.

## Per browser

Extension installed as each browser's install path does it: Chrome and Brave
from an external-extensions JSON pointing at a CRX packed with
`--pack-extension` (enabled, location 2); Firefox and LibreWolf from an
unsigned XPI in `<profile>/extensions/` with `xpinstall.signatures.required`
false and `autoDisableScopes` 0. Chromium was run with `--no-sandbox`
(the container cannot create user namespaces).

### Question 1: permissions, prompts, errors

| | Chrome 155 | Brave 1.97.57 | Developer Edition 159 | LibreWolf 158 |
| --- | --- | --- | --- | --- |
| WebSocket, no `host_permissions` | connects | connects | **fails, 1015** (upgraded to `wss://`) | **fails, 1015** |
| WebSocket, `http://127.0.0.1/*` or `ws://127.0.0.1/*` | connects | connects | fails, 1015 | fails, 1015 |
| WebSocket, CSP `script-src 'self'`, no `host_permissions` | connects | connects | connects | connects |
| `fetch`, no `host_permissions` | `TypeError: Failed to fetch` (CORS; the request reaches the server) | same | `NetworkError` (CORS) | same |
| `fetch`, `http://127.0.0.1/*` | 200, body readable | same | same | same |
| `fetch`, `ws://127.0.0.1/*` only | fails (CORS) | same | same | same |
| `permissions.contains` for the declared origin | true | true | true (listed under `optionalPermissions` in `extensions.json`, but granted) | same |
| Prompt for the extension (manifest warnings on the extensions page not checked) | none | none; default Shields did not block anything | none | none |

Evidence: `q1-*`, `csp-*`, `ff-*` runs. LibreWolf sends `Origin: null` on
`fetch(..., {mode: "no-cors"})` where Developer Edition sends the
`moz-extension://` origin; the WebSocket and CORS fetches carry the
extension origin in both.

### Question 2: does an open socket keep the background alive?

| | Chrome | Brave | Developer Edition | LibreWolf |
| --- | --- | --- | --- | --- |
| Socket open, server sends every 20 s, extension does nothing | alive all 9 min (worker listed in DevTools targets every 30 s; same instance before and after the server restart) | same | **stopped 30 s after the last extension event**; socket closed 1001 | same |
| Same, extension calls `runtime.getPlatformInfo()` per message | not needed | not needed | alive 8 min, one instance across the server restart | alive 5 min until the server was killed, stopped 30 s later, restarted by the alarm |
| Same, `storage.local.set()` per message (with an `onChanged` listener) | not needed | not needed | alive to the end of the 4-minute run | same |
| Socket open, no traffic | stopped at 30 s, socket closed 1001 | 30 s | 30 s | 30 s |

Evidence: `P-*` (ping), `S-*` (silent, 5 min), `K-*` and `L-*` (keep-alive).
The 1001 close comes from the browser; the stopped worker never runs
`onclose`.

In Developer Edition (every start) and LibreWolf (one start) the first
socket was dropped about 10 s after the browser started (EOF, code 1006),
in a fresh profile, without the worker stopping. The cause was not found;
the 30 s reconnect alarm covered it each time.

### Question 3: reconnect after the server restarts

The server was killed and restarted 15 s later.

| Worker at the kill | Reconnect | All four browsers |
| --- | --- | --- |
| connected (Chromium pings, Gecko keep-alive) | `onclose` set a one-shot 30 s alarm; it fired (starting the worker again if it had stopped meanwhile) and reconnected | 15 s after the restart (30 s after the kill) |
| stopped, periodic 1-minute alarm | the next tick started the worker; the top-level code connected | within 60 s |
| stopped, no periodic alarm | nothing; it stays disconnected until another event (bookmark change, browser start) wakes it | — |

Evidence: `P-*`, `A-*`, `L-*`. A periodic alarm of 0.5 minutes in Gecko
(`K-*-alarm30`) did not keep the event page alive: it was stopped and
restarted about once a minute.

### Question 4: `Origin`, and web pages

What the server saw on the WebSocket upgrade:

| Client | Chrome | Brave | Developer Edition | LibreWolf |
| --- | --- | --- | --- | --- |
| The extension | `chrome-extension://<id>` | same | `moz-extension://<uuid>` | same |
| Page `http://127.0.0.1:8001` | connects, `Origin: http://127.0.0.1:8001` | same | same | same |
| Page `http://172.17.0.x:8001` (private) | connects, its origin | same | same | **blocked** |
| Page `http://203.0.113.5:8001` (public, insecure) | **blocked**, no prompt ("the request client is not a secure context and the resource is in more-private address space `loopback`") | blocked | blocked | blocked |
| Page `https://203.0.113.5:8443` (public, secure) | **Local Network Access prompt** "wants to access other apps and services on this device"; the socket waits on it | same prompt, with "Remember my decision" | not tried | not tried |

The public address is `203.0.113.5` (TEST-NET-3) added to the container's
loopback interface (`--cap-add NET_ADMIN`). LibreWolf's private and public
page runs needed `dom.security.https_only_mode` off; with HTTPS-Only on,
those pages never load. Evidence: `q1-*`, `W-*`, `T-*` and their
screenshots.

## Consequences for the design

1. **WebSocket, not fetch.** It needs no host permission in any browser, so
   no install-time warning. Add `"content_security_policy":
   {"extension_pages": "script-src 'self'"}` to the manifest, or Firefox and
   LibreWolf will never connect.
2. **The server must authenticate each connection.** Any local process can
   connect with any `Origin`, and pages from `127.0.0.1`, private addresses,
   or (after one click) public HTTPS sites can connect from the browser.
   Check `Origin` against the bound profile's exact extension origin
   (`chrome-extension://<id>`, `moz-extension://<uuid>` from `prefs.js`) and
   reject anything else before reading a message, then require the
   per-profile token. The `Origin` check alone stops browser pages; the
   token stops other local processes.
3. **Keep-alive.** The server sends a message at least every 20 s. In
   Chromium that is enough. In Firefox the extension must also call an
   extension API when the message arrives; `runtime.getPlatformInfo()` is
   the cheapest tried.
4. **Reconnect with alarms, as today.** Keep the one-shot alarm in `onclose`
   and also run a periodic alarm (1 minute), so a worker stopped while the
   server was down still comes back. Bookmark events and `onStartup` wake
   the worker anyway, so a change made in the browser is never lost; only
   changes coming from the server can wait up to a minute.
5. **Bind on `127.0.0.1` only.** Nothing here needed another address.

## Not verified

- Firefox's Local Network Access prompt for a secure public page (only
  Chromium was tried with HTTPS) and whether Firefox would let a page
  through after it.
- Release Firefox (only signed add-ons), and Chrome or Brave with their own
  sandbox enabled.
- Lifetimes beyond 9 minutes, sleep and resume, and a server that is down
  when the browser starts (only a restart while running was tried).
- The cause of Gecko's socket drop about 10 s after start.
- Anything about firejail (questions 5 to 9, deferred).

## The scripts

All in [`localhost-transport-spike/`](localhost-transport-spike/):

- `Dockerfile`: image `spike-lt`, the dev image plus zip, xwd, ImageMagick,
  iproute2 and a user `u`.
- `server/`: a standard-library Go WebSocket server on `127.0.0.1:8765`
  (logs every upgrade's `Origin` and every close; `-ping 20s` sends a
  message every 20 s) and a page server on `:8001` (and HTTPS `:8443` with a
  throwaway certificate) whose `page.html` tries a fetch and a socket to
  `127.0.0.1:8765`.
- `ext/bg-body.js`: the probe extension's background; `exp.sh` writes its
  manifest and prepends its settings.
- `exp.sh`: one run inside the container, configured by environment (`B`,
  `HP`, `CSP`, `PING`, `PERIODIC`, `KA`, `DUR`, `RESTART`, `PAGES`, `PAGE`,
  `FFPREFS`, `MOZLOG`, `CHFLAGS`; see its header).
- `run.sh NAME VAR=value...`: starts one run in a throwaway container.
  Needs `$BIN/server` (`CGO_ENABLED=0 go build` in `server/`) and
  `$BIN/key.pem` (`openssl genrsa -out key.pem 2048`); writes to
  `$OUT/NAME`.
- `sum.sh DIR`: one line per logged event.

The runs, by name:

| Runs | Settings |
| --- | --- |
| `q1-<browser>-{none,http,ws}` | `HP=none/http/ws`, pages, 45 s |
| `csp-<browser>`, `ff-a..d` | `CSP=self`; Firefox socket variants (`localhost`, prefs, `MOZLOG`) |
| `P-<browser>` | `PING=20s DUR=540 RESTART=360 CSP=self` |
| `S-<browser>` | silent, `DUR=300` |
| `A-<browser>` | `PERIODIC=1 DUR=420 RESTART=200` |
| `K-<gecko>-{storage,api,alarm30}`, `L-<gecko>-api` | Gecko keep-alive; `L` is `DUR=480 RESTART=300` |
| `T-{chrome,brave}-pubtls`, `W-librewolf-*` | page checks (HTTPS public; LibreWolf one page per run) |
