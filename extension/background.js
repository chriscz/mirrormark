// mirrormark: sends this profile's bookmark tree to the local mirrormark
// server and applies the changes it sends back. It stays idle, with no
// connection, until `mirrormark install` binds the profile to a database.
// Chrome and Firefox install it in every bound profile, so it never
// uninstalls itself.
const V = 1;
const QUIET_MS = 500; // send a tree after this long without bookmark events
const REBIND_MS = 1000; // time for the old connection's host to let go of the database
// Reconnects wait for an alarm, which the browser fires no sooner than 30 s.
const MIN_BACKOFF = 30000;
const MAX_BACKOFF = 60000;
const DIAL_MS = 5000; // a port that does not finish the challenge in this time is skipped
// After a connection that worked closes, as when the server restarts, try
// again this soon, while the background is still running, rather than wait
// for the alarm. At most once per MAX_BACKOFF, so a server that keeps
// closing meets the back-off.
const QUICK_MS = 2000;
const RECONNECT = "reconnect"; // the one-shot alarm after a connection fails or closes
// A browser that stops an idle background closes its socket without
// running onclose; this alarm starts the background again, and it
// reconnects.
const TICK = "tick";
const NOT_RUNNING =
  "mirrormark is not running. Start it with `systemctl --user start mirrormark`, or run `mirrormark serve`.";
const CLOSED = "mirrormark restarted or stopped; reconnecting…";
const NOT_BOUND =
  "not bound yet: run `mirrormark install --profile <this profile>` again, with `--browser brave`, `firefox` or `librewolf` in those browsers.";
// What the errors a server can send before it has proven itself mean. Any
// program on the port could have sent one, so the popup shows these fixed
// texts and never the message that came with it.
const HELLO_ERRORS = {
  "bad-token": "this profile's binding is out of date; run `mirrormark install` for this profile again",
  "db-missing": "mirrormark has no database for this profile; run `mirrormark install` for this profile again",
  "bad-db": "this profile's binding names no valid database; run `mirrormark install` for this profile again",
  "unsupported-version": "mirrormark speaks another protocol version; restart the browser after upgrading mirrormark",
};
// The ports the server may listen on, in the order it tries them. The
// binary reads the same file.
const PORTS = fetch(chrome.runtime.getURL("ports.json")).then((r) => r.json());
const ROOTS = { "bookmarks-bar": "toolbar", other: "other", mobile: "mobile" };
// Firefox has no folderType; its top-level folders have fixed ids.
const FIREFOX_ROOTS = {
  toolbar_____: "toolbar",
  unfiled_____: "other",
  mobile______: "mobile",
  menu________: "menu",
};
const [, BROWSER = "Chrome", VERSION = ""] =
  navigator.userAgent.match(/(Firefox|Chrome)\/([\d.]+)/) ?? [];
// Separators exist only in Firefox. The host never sees them: they hold
// nothing to sync, and Chrome could not hold them.
const isSeparator = (n) => n.type === "separator";

let sock = null;
let connecting = null;
let quiet = null;
let unsent = false; // bookmark events since the last tree; the host knows
let rebinding = null; // the timer that connects after a new binding
let binding = 0; // counts new bindings, so a connect can tell it is stale
let backoff = MIN_BACKOFF;
let quickAt = 0; // when the last quick retry was set
let queue = Promise.resolve();
const refs = new Map(); // create ref -> new bookmark id, for later ops' parents

// Chrome starts the service worker for these events and the top-level
// connect() below runs. Without an onStartup listener the worker does not
// start when a profile opens.
chrome.runtime.onStartup.addListener(() => connect());
chrome.runtime.onInstalled.addListener(() => connect());
// The worker stops about 30 s after its socket closes, and its timers with
// it, so a reconnect waits for an alarm, which starts the worker again.
chrome.alarms.onAlarm.addListener(async (a) => {
  if (a.name === TICK) {
    connect(); // which does nothing while connected or while a reconnect waits
    return;
  }
  if (a.name !== RECONNECT) return;
  await chrome.alarms.clear(RECONNECT);
  connect();
});
// The popup asks for a connection when it opens, so that the user need
// not wait for the alarm. It is answered once the attempt has ended.
chrome.runtime.onMessage.addListener((m, sender, reply) => {
  if (m?.type !== "connect" || sender.id !== chrome.runtime.id) return;
  connectNow().finally(() => reply(true));
  return true;
});
chrome.alarms.get(TICK).then((a) => a ?? chrome.alarms.create(TICK, { periodInMinutes: 1 }));
// Firefox lacks onChildrenReordered and the import events; adding a
// listener to a missing one would stop this script here.
for (const e of [
  chrome.bookmarks.onCreated,
  chrome.bookmarks.onRemoved,
  chrome.bookmarks.onChanged,
  chrome.bookmarks.onMoved,
  chrome.bookmarks.onChildrenReordered,
  chrome.bookmarks.onImportEnded,
]) {
  e?.addListener(changed);
}
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local" || !(changes.db || changes.token || changes.boundAt)) return;
  // A new binding: reconnect so the host sees a fresh hello. The old host
  // holds the database's lock until it sees the socket close; connecting at
  // once would meet db-locked and the long back-off, so wait a moment.
  binding++;
  if (sock) sock.close();
  sock = null;
  backoff = MIN_BACKOFF;
  clearTimeout(rebinding);
  rebinding = setTimeout(async () => {
    await chrome.alarms.clear(RECONNECT);
    await connecting; // a connect begun before the binding gives up
    rebinding = null;
    connect();
  }, REBIND_MS);
});
connect();

function changed() {
  if (!sock) {
    connect(); // which waits out a pending reconnect
    return;
  }
  // Tell the host at once, so it runs no round on the tree before this
  // event while the quiet time runs.
  if (!unsent) post(sock, { v: V, type: "changed" });
  unsent = true;
  clearTimeout(quiet);
  quiet = setTimeout(sendTree, QUIET_MS);
}

// connect opens the socket unless one is open or opening. The finally runs
// after the assignment even when open returns at once; clearing connecting
// inside open would run first and leave a settled promise behind, and no
// later connect would open a socket.
function connect() {
  connecting ??= open().finally(() => {
    connecting = null;
  });
  return connecting;
}

// connectNow connects without waiting for a pending reconnect alarm,
// unless the server refused this profile, which it would do again. An
// attempt already in flight is the one the caller waits for.
async function connectNow() {
  if (connecting && (await connecting)) return;
  if (sock || rebinding) return;
  const { status } = await chrome.storage.local.get("status");
  if (status?.refused) return;
  await chrome.alarms.clear(RECONNECT);
  await connect();
}

// open dials unless it need not or must not yet, and reports whether it
// dialed.
async function open() {
  if (sock || rebinding) return;
  // After a failed connection the alarm sets the pace, not bookmark events
  // or a restarted worker.
  if (await chrome.alarms.get(RECONNECT)) return;
  const bound = binding;
  const { db, token } = await chrome.storage.local.get(["db", "token"]);
  if (!db) return; // not bound: stay dormant
  if (!token) {
    // Bound by a build that had no tokens: nothing could prove itself.
    await setStatus({ error: NOT_BOUND, problems: [] });
    return;
  }
  const { sock: s, failure, hostError: refused } = await dial(db, token);
  if (bound !== binding || sock || rebinding) {
    s?.close(); // rebound meanwhile
    return true;
  }
  if (!s) {
    await setStatus({ error: failure ?? NOT_RUNNING, problems: [], refused: !!refused });
    // A server that refused this profile will refuse it again; don't ask
    // every 30 s.
    chrome.alarms.create(RECONNECT, { when: Date.now() + (refused ? MAX_BACKOFF : MIN_BACKOFF) });
    return true;
  }
  refs.clear();
  const opened = Date.now();
  let hostError = false;
  sock = s;
  s.onmessage = (ev) => {
    let m;
    try {
      m = JSON.parse(ev.data);
    } catch (e) {
      console.warn("mirrormark: bad message", e);
      return;
    }
    if (m.type === "ping") {
      // Gecko stops an event page 30 s after its last extension event,
      // socket traffic or not; an API call counts as one.
      chrome.runtime.getPlatformInfo();
      return;
    }
    if (m.type === "error") {
      // The host stops after an error; don't reconnect every 30 s. Set
      // here, not in the queue, so the close that follows already waits
      // the longer time.
      hostError = true;
      backoff = MAX_BACKOFF;
    }
    queue = queue
      .then(() => onMessage(s, m))
      .catch((e) => console.warn("mirrormark: handling", m.type, "failed:", e));
  };
  s.onclose = (ev) => {
    if (sock !== s) return; // replaced by a newer connection
    sock = null;
    console.info("mirrormark: the connection closed with code", ev.code);
    // The host's own error says more than the close.
    if (!hostError) setStatus({ error: CLOSED });
    if (Date.now() - opened > MAX_BACKOFF) backoff = MIN_BACKOFF;
    // The alarm stays as the fallback, for a background stopped before
    // the quick retry runs.
    chrome.alarms.create(RECONNECT, { when: Date.now() + backoff });
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
    if (hostError || Date.now() - quickAt < MAX_BACKOFF) return;
    quickAt = Date.now();
    setTimeout(async () => {
      await chrome.alarms.clear(RECONNECT);
      connect();
    }, QUICK_MS);
  };
  // The hello and the proof went out in dial, so no tree can precede them.
  await sendTree();
  return true;
}

// dial returns { sock } for the first port whose server proves it holds
// this profile's token, once the extension has sent its own proof. If none
// does, it returns the answer to show instead: the first { failure,
// hostError } for a known error, else the first { failure } for a wrong
// proof or an unexpected answer, else {} if nothing answered. A known
// error goes first because a program on another port must not hide why
// the real server refused this profile. A port where nothing listens fails
// at once, and so does one whose server refuses the upgrade, such as
// another user's mirrormark, which does not know this profile's origin.
async function dial(db, token) {
  const rank = (r) => (r.hostError ? 2 : r.failure ? 1 : 0);
  let best = {};
  for (const p of await PORTS) {
    const r = await challenge(p, db, token);
    if (r.sock) return r;
    if (rank(r) > rank(best)) best = r;
  }
  return best;
}

// challenge runs the hello on port p. The server must prove it holds the
// token before the extension proves it does, so a program posing as
// mirrormark learns only the database id and a nonce it cannot use. The
// token itself never goes on the socket.
function challenge(p, db, token) {
  return new Promise((resolve) => {
    const s = new WebSocket(`ws://127.0.0.1:${p}/`);
    const nonceE = hex(crypto.getRandomValues(new Uint8Array(32)));
    let over = false;
    const done = (r) => {
      if (over) return;
      over = true;
      clearTimeout(t);
      s.onopen = s.onmessage = s.onerror = s.onclose = null;
      if (!r.sock) s.close();
      resolve(r);
    };
    const t = setTimeout(() => done({}), DIAL_MS);
    s.onerror = s.onclose = () => done({});
    s.onopen = () => post(s, { v: V, type: "hello", db, nonce: nonceE, browser: BROWSER.toLowerCase() + "/" + VERSION });
    s.onmessage = async (ev) => {
      let m;
      try {
        m = JSON.parse(ev.data);
      } catch {
        m = null;
      }
      if (typeof m !== "object" || m === null) m = {};
      if (m.type === "ping") return;
      const unexpected = { failure: `unexpected answer on port ${p}; the program there may not be mirrormark` };
      if (m.type === "error") {
        if (!Object.hasOwn(HELLO_ERRORS, m.code)) return done(unexpected);
        return done({ failure: `${m.code}: ${HELLO_ERRORS[m.code]} (port ${p})`, hostError: true });
      }
      if (m.type !== "challenge") return done(unexpected);
      // A plain comparison is enough here: each attempt has a new nonce,
      // so timing it teaches nothing about the next.
      if (m.proof !== (await mac(token, `server:${p}:${nonceE}`))) {
        return done({ failure: `the program on port ${p} could not prove it is your mirrormark` });
      }
      const proof = await mac(token, `extension:${m.nonce}`);
      if (over) return; // timed out meanwhile
      post(s, { v: V, type: "proof", proof });
      done({ sock: s });
    };
  });
}

// mac is HMAC-SHA256 of msg, keyed by the token as text, in lowercase hex,
// as the server computes it.
async function mac(token, msg) {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(token), { name: "HMAC", hash: "SHA-256" }, false, [
    "sign",
  ]);
  return hex(new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode(msg))));
}

function hex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function post(s, m) {
  if (s.readyState !== WebSocket.OPEN) {
    console.warn("mirrormark: cannot send", m.type, "on a closed connection");
    return;
  }
  s.send(JSON.stringify(m));
}

async function sendTree() {
  const p = sock;
  if (!p) return;
  unsent = false; // an event from here on sends "changed" after this tree
  const [root] = await chrome.bookmarks.getTree();
  const nodes = [];
  const walk = (n, parent) =>
    (n.children ?? [])
      .filter((c) => !isSeparator(c))
      .forEach((c, index) => {
        nodes.push({
          id: c.id,
          parent,
          index,
          title: c.title,
          url: c.url,
          root: parent === null ? (ROOTS[c.folderType] ?? FIREFOX_ROOTS[c.id]) : undefined,
          syncing: c.syncing || undefined,
        });
        walk(c, c.id);
      });
  walk(root, null);
  post(p, { v: V, type: "tree", nodes });
}

async function onMessage(p, m) {
  if (m.type === "error") {
    // Why the host stopped; shown even if the socket has closed since.
    // It would stop again, so opening the popup does not hurry the retry.
    await setStatus({ error: `${m.code}: ${m.message}`, problems: [], refused: true });
    return;
  }
  if (p !== sock) return; // from a connection since closed
  if (m.type === "apply") {
    // One at a time, in order: each op assumes the ones before it landed.
    // After the first failure the rest are not run.
    const results = [];
    let failed = null;
    for (const op of m.ops ?? []) {
      if (failed) {
        results.push({ ok: false, error: "not run" });
        continue;
      }
      const r = await applyOp(op);
      if (!r.ok) failed = r;
      results.push(r);
    }
    post(p, { v: V, type: "applied", batch: m.batch, results });
    await sendTree();
  } else if (m.type === "status") {
    // The end of a round: the host runs, and these are all that is wrong.
    backoff = MIN_BACKOFF;
    await setStatus({ error: null, problems: m.problems ?? [], refused: false });
  }
}

// realIndex turns the host's index, which counts only siblings that are not
// separators, into the browser's. An index past the end means the end.
// The host never moves a node within its own parent (reordering stays
// local), where Firefox and Chrome read the index differently.
async function realIndex(parentId, index) {
  if (index === undefined) return undefined;
  const kids = await chrome.bookmarks.getChildren(parentId);
  const shown = kids.filter((k) => !isSeparator(k));
  return index < shown.length ? shown[index].index : kids.length;
}

// matches reports whether node n is as the host expects. Fields the host
// leaves out are not checked.
function matches(n, expect) {
  if (!expect) return true;
  if (expect.parent !== undefined && n.parentId !== expect.parent) return false;
  if (expect.title !== undefined && n.title !== expect.title) return false;
  if (expect.url !== undefined && (n.url ?? "") !== expect.url) return false;
  return true;
}

// applyOp runs one op. update, move and remove first check that the node is
// as the host expects, so an edit the user makes during a batch survives.
async function applyOp(op) {
  try {
    const parent = op.parent && (refs.get(op.parent) ?? op.parent);
    switch (op.op) {
      case "create": {
        const n = await chrome.bookmarks.create({
          parentId: parent,
          index: await realIndex(parent, op.index),
          title: op.title,
          url: op.url,
        });
        refs.set(op.ref, n.id);
        return { ok: true, id: n.id };
      }
      case "update": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (!matches(n, op.expect)) return { ok: false, error: "stale" };
        const changes = {};
        if (op.title !== undefined) changes.title = op.title;
        if (op.url !== undefined) changes.url = op.url;
        await chrome.bookmarks.update(op.id, changes);
        return { ok: true };
      }
      case "move": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (!matches(n, op.expect)) return { ok: false, error: "stale" };
        await chrome.bookmarks.move(op.id, { parentId: parent, index: await realIndex(parent, op.index) });
        return { ok: true };
      }
      case "remove": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (!matches(n, op.expect)) return { ok: false, error: "stale" };
        // Never remove a whole subtree. The host removes a folder only
        // after everything it knows inside, so a folder that still holds
        // anything else holds something it means to keep, and neither
        // browser removes a folder that is not empty. The host cannot see
        // separators, so remove them first, but only when nothing else is
        // left: otherwise the remove fails and the separators stay.
        if (n.url === undefined) {
          const kids = await chrome.bookmarks.getChildren(op.id);
          if (kids.every(isSeparator)) {
            for (const k of kids) await chrome.bookmarks.remove(k.id);
          }
        }
        await chrome.bookmarks.remove(op.id);
        return { ok: true };
      }
    }
    return { ok: false, error: "unknown op " + op.op };
  } catch (e) {
    return { ok: false, error: String(e) };
  }
}

async function setStatus(s) {
  const { status } = await chrome.storage.local.get("status");
  await chrome.storage.local.set({ status: { ...status, ...s } });
}
