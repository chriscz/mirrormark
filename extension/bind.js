// Opened by `mirrormark install` in one profile as
// bind.html#<database id>.<token>. The page is not web-accessible, so web
// pages cannot open it.
const [db, token] = decodeURIComponent(location.hash.slice(1)).split(".");
const msg = document.getElementById("msg");
if (!/^[a-z0-9][a-z0-9-]*$/.test(db) || !/^[0-9a-f]{64}$/.test(token ?? "")) {
  msg.textContent = "This link does not name a mirrormark database.";
} else {
  // boundAt changes even when db does not, so a rebind always reconnects.
  chrome.storage.local.set({ db, token, boundAt: Date.now() }).then(() => {
    msg.textContent = `This profile now syncs as ${db}. You can close this tab.`;
    setTimeout(() => window.close(), 1500);
  });
}
