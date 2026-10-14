const HOST = "com.github.chriscz.mirrormark";
const api = globalThis.browser ?? globalThis.chrome;
let port = null;
const log = (...a) => console.log("SPIKE", ...a);
chrome.runtime.onStartup.addListener(() => connect("onStartup"));
chrome.runtime.onInstalled.addListener((d) => connect("onInstalled:" + d.reason));
const evs = ["onCreated","onRemoved","onChanged","onMoved","onChildrenReordered","onImportBegan","onImportEnded"];
const present = {};
for (const e of evs) {
  present[e] = !!chrome.bookmarks[e];
  if (chrome.bookmarks[e]) chrome.bookmarks[e].addListener((...a) => port && port.postMessage({type: "event", e, a}));
}
chrome.storage.onChanged.addListener((c, area) => { if (area === "local" && c.db) { if (port) port.disconnect(); port = null; connect("rebind"); } });
connect("top");
async function connect(why) {
  if (port) return;
  const { db } = await chrome.storage.local.get("db");
  port = chrome.runtime.connectNative(HOST);
  port.onDisconnect.addListener((p) => { log("disconnect", p.error?.message, chrome.runtime.lastError?.message); port = null; });
  let q = Promise.resolve(); port.onMessage.addListener((m) => { q = q.then(() => onMsg(m)); });
  port.postMessage({ type: "hello", why, db: db ?? null, ua: navigator.userAgent, url: location.href, present,
    hasBrowser: typeof globalThis.browser, isWorker: typeof window === "undefined", runtimeId: chrome.runtime.id, uuidURL: chrome.runtime.getURL("") });
}
async function onMsg(m) {
  let r;
  try {
    switch (m.cmd) {
      case "tree": r = await chrome.bookmarks.getTree(); break;
      case "create": r = await chrome.bookmarks.create(m.arg); break;
      case "remove": r = await chrome.bookmarks.remove(m.id); break;
      case "removeTree": r = await chrome.bookmarks.removeTree(m.id); break;
      case "move": r = await chrome.bookmarks.move(m.id, m.arg); break;
      case "update": r = await chrome.bookmarks.update(m.id, m.arg); break;
      case "children": r = await chrome.bookmarks.getChildren(m.id); break;
      case "get": r = await chrome.bookmarks.get(m.id); break;
      case "storage": r = await chrome.storage.local.get(null); break;
      case "alarm": chrome.alarms.create("a", {when: Date.now() + 1000}); chrome.alarms.onAlarm.addListener(a => port && port.postMessage({type:"alarm", a})); r = "set"; break;
      case "bmtest": r = await bmtest(); break;
      case "ping": r = Date.now(); break;
      default: r = "unknown";
    }
    port.postMessage({ type: "reply", n: m.n, cmd: m.cmd, ok: true, r });
  } catch (e) {
    port.postMessage({ type: "reply", n: m.n, cmd: m.cmd, ok: false, err: String(e) });
  }
}

async function bmtest() {
  const B = chrome.bookmarks, out = {};
  const t = async (k, f) => { try { out[k] = await f(); } catch (e) { out[k] = "ERR " + String(e); } };
  const [root] = await B.getTree();
  out.root = { id: root.id, title: root.title, keys: Object.keys(root), kids: root.children.map(c => ({ id: c.id, title: c.title, folderType: c.folderType, type: c.type, unmodifiable: c.unmodifiable, keys: Object.keys(c), n: c.children?.length })) };
  const tb = root.children.find(c => c.id === "toolbar_____")?.id ?? "1"; const other = root.children.find(c => c.id === "menu________")?.id ?? "2";
  const f = await B.create({ parentId: tb, title: "F" });
  out.folderNode = f;
  const a = await B.create({ parentId: f.id, title: "a", url: "https://a.example/" });
  let sep; try { sep = await B.create({ parentId: f.id, type: "separator" }); } catch (e) { sep = "ERR " + e; }
  out.sep = sep;
  const b = await B.create({ parentId: f.id, title: "b", url: "https://b.example/" });
  const c = await B.create({ parentId: f.id, title: "c", url: "https://c.example/" });
  await t("sepCreateViaUrlless", async () => (await B.create({ parentId: f.id, title: "" })).type);
  await t("childrenAfterCreate", async () => (await B.getChildren(f.id)).map(n => [n.index, n.type, n.title]));
  // move semantics: move a (index 0) to index 3 within same parent
  await t("moveSameParent0to3", async () => { const r = await B.move(a.id, { parentId: f.id, index: 3 }); return { returnedIndex: r.index, order: (await B.getChildren(f.id)).map(n => n.title || n.type) }; });
  await t("moveIndexBeyond", async () => { const r = await B.move(b.id, { parentId: f.id, index: 99 }); return r.index; });
  await t("createIndexBeyond", async () => (await B.create({ parentId: f.id, index: 99, title: "z", url: "https://z.example/" })).index);
  await t("removeNonEmptyFolder", async () => { await B.remove(f.id); return "removed!"; });
  await t("folderStillThere", async () => (await B.get(f.id)).length);
  await t("updateSeparatorTitle", async () => (await B.update(typeof sep === "object" ? sep.id : "x", { title: "x" })).title);
  await t("removeRoot", async () => { await B.remove(tb); return "removed root!"; });
  await t("createInRoot", async () => (await B.create({ parentId: root.id, title: "X" })).id);
  await t("moveRoot", async () => (await B.move(tb, { parentId: other })).id);
  await t("updateRootTitle", async () => (await B.update(tb, { title: "T" })).title);
  await t("dataUrl", async () => (await B.create({ parentId: f.id, title: "d", url: "data:text/plain,hi" })).url);
  await t("placeUrl", async () => (await B.create({ parentId: f.id, title: "p", url: "place:sort=8&maxResults=10" })).url);
  await t("javascriptUrl", async () => (await B.create({ parentId: f.id, title: "j", url: "javascript:void(0)" })).url);
  await t("aboutUrl", async () => (await B.create({ parentId: f.id, title: "ab", url: "about:config" })).url);
  await t("fileUrl", async () => (await B.create({ parentId: f.id, title: "fi", url: "file:///etc/hosts" })).url);
  await t("chromeUrl", async () => (await B.create({ parentId: f.id, title: "ch", url: "chrome://settings/" })).url);
  await t("nonCanonUrl", async () => (await B.create({ parentId: f.id, title: "nc", url: "HTTPS://Example.COM" })).url);
  await t("updateFolderWithUrl", async () => (await B.update(f.id, { url: "https://x/" })).url);
  await t("searchQuery", async () => (await B.search({ url: "https://a.example/" })).length);
  await t("getSubTree", async () => Object.keys((await B.getSubTree(f.id))[0]));
  await t("removeTree", async () => { await B.removeTree(f.id); return "ok"; });
  out.finalTree = (await B.getTree())[0].children.map(c => [c.id, c.title, c.children.length]);
  return out;
}
