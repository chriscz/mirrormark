const $ = (id) => document.getElementById(id);
let asking = true; // until the background answers the request below

async function show() {
  const { db, status } = await chrome.storage.local.get(["db", "status"]);
  if (!db) {
    $("db").textContent = "Not bound. Run `mirrormark install` to bind this profile.";
    $("busy").hidden = true;
    $("error").textContent = "";
    return;
  }
  $("db").textContent = `Syncs as ${db}.`;
  // Not connected at last report, and the background is trying again: say
  // so instead of the last failure. A refusal is not retried at once.
  const busy = asking && !!status?.error && !status.refused;
  $("busy").hidden = !busy;
  const problems = (status?.problems ?? []).map((p) => `${p.code}: ${p.message}`);
  $("error").textContent = busy ? "" : [status?.error, ...problems].filter(Boolean).join("\n");
}

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local") show();
});
show();
// Connect now rather than at the background's next retry. The answer
// comes once the attempt has ended, and the status it left shows.
chrome.runtime
  .sendMessage({ type: "connect" })
  .catch((e) => console.warn("mirrormark: cannot reach the background:", e))
  .finally(() => {
    asking = false;
    show();
  });
