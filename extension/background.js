// mirrormark: sends this profile's bookmark tree to the local mirrormark
// program and applies the changes it sends back. It stays idle, with no
// connection, until `mirrormark install` binds the profile to a database.
// Chrome and Firefox install it in every bound profile, so it never
// uninstalls itself.
const HOST = "com.github.chriscz.mirrormark";
const V = 1;
const QUIET_MS = 500; // send a tree after this long without bookmark events
const REBIND_MS = 1000; // time for the old host to let go of the database
// Reconnects wait for an alarm, which Chrome fires no sooner than 30 s.
const MIN_BACKOFF = 30000;
const MAX_BACKOFF = 60000;
const RECONNECT = "reconnect"; // the alarm's name
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

let port = null;
let connecting = null;
let quiet = null;
let unsent = false; // bookmark events since the last tree; the host knows
let rebinding = null; // the timer that connects after a new binding
let binding = 0; // counts new bindings, so a connect can tell it is stale
let backoff = MIN_BACKOFF;
let queue = Promise.resolve();
const refs = new Map(); // create ref -> new bookmark id, for later ops' parents

// Chrome starts the service worker for these events and the top-level
// connect() below runs. Without an onStartup listener the worker does not
// start when a profile opens. An open native port keeps the worker alive.
chrome.runtime.onStartup.addListener(() => connect());
chrome.runtime.onInstalled.addListener(() => connect());
// The worker stops about 30 s after its port closes, and its timers with
// it, so a reconnect waits for an alarm, which starts the worker again.
chrome.alarms.onAlarm.addListener(async (a) => {
  if (a.name !== RECONNECT) return;
  await chrome.alarms.clear(RECONNECT);
  connect();
});
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
  if (area !== "local" || !(changes.db || changes.boundAt)) return;
  // A new binding: reconnect so the host sees a fresh hello. The old host
  // holds the database's lock until it sees the port close; connecting at
  // once would meet db-locked and the long back-off, so wait a moment.
  binding++;
  if (port) port.disconnect();
  port = null;
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
  if (!port) {
    connect(); // which waits out a pending reconnect
    return;
  }
  // Tell the host at once, so it runs no round on the tree before this
  // event while the quiet time runs.
  if (!unsent) post(port, { v: V, type: "changed" });
  unsent = true;
  clearTimeout(quiet);
  quiet = setTimeout(sendTree, QUIET_MS);
}

// connect opens the port unless one is open or opening. The finally runs
// after the assignment even when open returns at once; clearing connecting
// inside open would run first and leave a settled promise behind, and no
// later connect would open a port.
function connect() {
  connecting ??= open().finally(() => {
    connecting = null;
  });
  return connecting;
}

async function open() {
  if (port || rebinding) return;
  // After a failed connection the alarm sets the pace, not bookmark events
  // or a restarted worker.
  if (await chrome.alarms.get(RECONNECT)) return;
  const bound = binding;
  const { db } = await chrome.storage.local.get("db");
  if (!db) return; // not bound: stay dormant
  if (bound !== binding || port || rebinding) return; // rebound meanwhile
  refs.clear();
  const p = chrome.runtime.connectNative(HOST);
  const opened = Date.now();
  let hostError = false;
  port = p;
  p.onMessage.addListener((m) => {
    if (m.type === "error") {
      // The host stops after an error; don't restart it every 30 s. Set
      // here, not in the queue, so the disconnect that follows already
      // waits the longer time.
      hostError = true;
      backoff = MAX_BACKOFF;
    }
    queue = queue
      .then(() => onMessage(p, m))
      .catch((e) => console.warn("mirrormark: handling", m.type, "failed:", e));
  });
  p.onDisconnect.addListener((dp) => {
    // Firefox puts the reason on the port; Chrome in runtime.lastError.
    const err = dp?.error?.message ?? chrome.runtime.lastError?.message;
    if (port !== p) return; // replaced by a newer connection
    port = null;
    // The host's own error says more than "Native host has exited".
    if (err && !hostError) setStatus({ error: err });
    if (Date.now() - opened > MAX_BACKOFF) backoff = MIN_BACKOFF;
    chrome.alarms.create(RECONNECT, { when: Date.now() + backoff });
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
  });
  // The hello goes out in the same task that sets port, so no tree can
  // precede it.
  p.postMessage({ v: V, type: "hello", db, browser: BROWSER.toLowerCase() + "/" + VERSION });
  await sendTree();
}

function post(p, m) {
  try {
    p.postMessage(m);
  } catch (e) {
    console.warn("mirrormark: cannot send", m.type, e); // the port closed
  }
}

async function sendTree() {
  const p = port;
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
    // Why the host stopped; shown even if the port has closed since.
    await setStatus({ error: `${m.code}: ${m.message}`, problems: [] });
    return;
  }
  if (p !== port) return; // from a connection since closed
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
    await setStatus({ error: null, problems: m.problems ?? [] });
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
