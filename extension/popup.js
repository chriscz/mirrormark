chrome.storage.local.get(["db", "status"]).then(({ db, status }) => {
  const $ = (id) => document.getElementById(id);
  if (!db) {
    $("db").textContent = "Not bound. Run `mirrormark install` to bind this profile.";
    return;
  }
  $("db").textContent = `Syncs as ${db}.`;
  const problems = (status?.problems ?? []).map((p) => `${p.code}: ${p.message}`);
  $("error").textContent = [status?.error, ...problems].filter(Boolean).join("\n");
});
