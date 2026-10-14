chrome.storage.local.get(["db", "status"]).then(({ db, status }) => {
  const $ = (id) => document.getElementById(id);
  if (!db) {
    $("db").textContent = "Not bound. Run `mirrormark install` to bind this profile.";
    return;
  }
  $("db").textContent = `Syncs as ${db}.`;
  if (status?.error) $("error").textContent = status.error;
});
