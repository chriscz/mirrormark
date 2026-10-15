// Probe background. CFG is prepended by exp.sh:
//   CFG.periodic: minutes for a periodic "tick" alarm, 0 = none.
//   CFG.ws: the WebSocket base URL, normally ws://127.0.0.1:8765.
//   CFG.ka: what to do on each server message, to try to keep a Firefox
//     event page alive: "" nothing, "storage" storage.local.set (with a
//     storage.onChanged listener), "api" runtime.getPlatformInfo().
// It connects to CFG.ws on every start, reconnects on close
// through a one-shot 30 s alarm (as mirrormark does today), and reports
// through no-cors POSTs to /log, which the server logs whatever the
// permissions. It sends nothing on the socket after its hello, so that
// only the server's pings (if any) are socket traffic.
const API = "127.0.0.1:8765";
const INST = Math.random().toString(36).slice(2, 8); // one per worker start
const T0 = Date.now();
let ws = null;
const B = globalThis.browser ?? chrome;
const rep = (k, v) =>
  fetch(`http://${API}/log`, {
    method: "POST",
    mode: "no-cors",
    body: JSON.stringify({ inst: INST, up_s: Math.round((Date.now() - T0) / 1000), k, v }),
  }).catch(() => {});

function connect(why) {
  if (ws && ws.readyState <= 1) return;
  let s;
  try {
    s = new WebSocket(`${CFG.ws}/ws?inst=${INST}&why=${why}`);
  } catch (e) {
    rep("ws-throw", String(e));
    return;
  }
  ws = s;
  s.onopen = () => s.send(`hello inst=${INST} why=${why} up_s=${Math.round((Date.now() - T0) / 1000)}`);
  s.onmessage = () => {
    if (CFG.ka === "storage") B.storage.local.set({ ka: Date.now() });
    if (CFG.ka === "api") B.runtime.getPlatformInfo();
  };
  s.onerror = () => rep("ws-error", why);
  s.onclose = (e) => {
    rep("ws-closed", { code: e.code, why });
    if (ws === s) ws = null;
    B.alarms.create("reconnect", { when: Date.now() + 30000 });
  };
}

if (CFG.ka === "storage") B.storage.onChanged.addListener(() => {});
B.runtime.onStartup.addListener(() => connect("onStartup"));
B.runtime.onInstalled.addListener(() => connect("onInstalled"));
B.alarms.onAlarm.addListener((a) => connect("alarm-" + a.name));
if (CFG.periodic) B.alarms.create("tick", { periodInMinutes: CFG.periodic });
connect("toplevel");

(async () => {
  const perms = {};
  for (const o of ["http://127.0.0.1/*", "ws://127.0.0.1/*"]) {
    try {
      perms[o] = await B.permissions.contains({ origins: [o] });
    } catch (e) {
      perms[o] = "err " + e.message;
    }
  }
  let f;
  try {
    const r = await fetch(`http://${API}/probe`);
    f = { status: r.status, body: await r.text() };
  } catch (e) {
    f = "err " + e;
  }
  rep("start", { ua: navigator.userAgent, url: location.href, perms, fetch: f, cfg: CFG });
})();
