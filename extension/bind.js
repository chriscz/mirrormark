// Opened by `mirrormark install` in one profile as bind.html#<database id>.
// The page is not web-accessible, so web pages cannot open it.
const db = decodeURIComponent(location.hash.slice(1));
const msg = document.getElementById("msg");
if (!/^[a-z0-9][a-z0-9-]*$/.test(db)) {
  msg.textContent = "This link does not name a mirrormark database.";
} else {
  // boundAt changes even when db does not, so a rebind always reconnects.
  chrome.storage.local.set({ db, boundAt: Date.now() }).then(() => {
    msg.textContent = `This profile now syncs as ${db}. You can close this tab.`;
    setTimeout(() => window.close(), 1500);
  });
}
