// mirrormark: sends this profile's bookmark tree to the local mirrormark
// program and applies the changes it sends back. It stays idle, with no
// connection, until `mirrormark install` binds the profile to a database.
// Chrome installs it in every profile, so it never uninstalls itself.
const HOST = "com.github.chriscz.mirrormark";
const V = 1;
const QUIET_MS = 500; // send a tree after this long without bookmark events
const MIN_BACKOFF = 5000;
const MAX_BACKOFF = 60000;
const ROOTS = { "bookmarks-bar": "toolbar", other: "other", mobile: "mobile" };

let port = null;
let connecting = null;
let quiet = null;
let retry = null;
let backoff = MIN_BACKOFF;
let queue = Promise.resolve();
const refs = new Map(); // create ref -> new bookmark id, for later ops' parents

// Chrome starts the service worker for these events and the top-level
// connect() below runs. Without an onStartup listener the worker does not
// start when a profile opens. An open native port keeps the worker alive.
chrome.runtime.onStartup.addListener(() => connect());
chrome.runtime.onInstalled.addListener(() => connect());
for (const e of [
  chrome.bookmarks.onCreated,
  chrome.bookmarks.onRemoved,
  chrome.bookmarks.onChanged,
  chrome.bookmarks.onMoved,
  chrome.bookmarks.onChildrenReordered,
  chrome.bookmarks.onImportEnded,
]) {
  e.addListener(changed);
}
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local" || !(changes.db || changes.boundAt)) return;
  // A new binding: reconnect so the host sees a fresh hello.
  if (port) port.disconnect();
  port = null;
  backoff = MIN_BACKOFF;
  connect();
});
connect();

function changed() {
  if (!port) {
    connect();
    return;
  }
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
  if (port) return;
  const { db } = await chrome.storage.local.get("db");
  if (!db) return; // not bound: stay dormant
  clearTimeout(retry);
  refs.clear();
  const p = chrome.runtime.connectNative(HOST);
  const opened = Date.now();
  let hostError = false;
  port = p;
  p.onMessage.addListener((m) => {
    if (m.type === "error") hostError = true;
    queue = queue
      .then(() => onMessage(p, m))
      .catch((e) => console.warn("mirrormark: handling", m.type, "failed:", e));
  });
  p.onDisconnect.addListener(() => {
    const err = chrome.runtime.lastError?.message;
    if (port !== p) return; // replaced by a newer connection
    port = null;
    // The host's own error says more than "Native host has exited".
    if (err && !hostError) setStatus({ error: err });
    if (Date.now() - opened > MAX_BACKOFF) backoff = MIN_BACKOFF;
    retry = setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
  });
  const version = navigator.userAgent.match(/Chrome\/([\d.]+)/)?.[1] ?? "";
  // The hello goes out in the same task that sets port, so no tree can
  // precede it.
  p.postMessage({ v: V, type: "hello", db, browser: "chrome/" + version });
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
  const [root] = await chrome.bookmarks.getTree();
  const nodes = [];
  const walk = (n, parent) =>
    (n.children ?? []).forEach((c, index) => {
      nodes.push({
        id: c.id,
        parent,
        index,
        title: c.title,
        url: c.url,
        root: parent === null ? ROOTS[c.folderType] : undefined,
        syncing: c.syncing || undefined,
      });
      walk(c, c.id);
    });
  walk(root, null);
  post(p, { v: V, type: "tree", nodes });
}

async function onMessage(p, m) {
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
  } else if (m.type === "error") {
    // The host exits after some errors; don't restart it every few seconds.
    backoff = MAX_BACKOFF;
    await setStatus({ error: `${m.code}: ${m.message}` });
  }
}

async function clamp(parentId, index) {
  if (index === undefined) return undefined;
  const kids = await chrome.bookmarks.getChildren(parentId);
  return Math.min(index, kids.length);
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
          index: await clamp(parent, op.index),
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
        await chrome.bookmarks.move(op.id, { parentId: parent, index: await clamp(parent, op.index) });
        return { ok: true };
      }
      case "remove": {
        const [n] = await chrome.bookmarks.get(op.id);
        if (!matches(n, op.expect)) return { ok: false, error: "stale" };
        // Never removeTree: a folder that is not empty still holds something
        // the host means to keep, and Chrome then refuses the remove.
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
