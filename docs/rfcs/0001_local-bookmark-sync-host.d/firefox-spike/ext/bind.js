const db = decodeURIComponent(location.hash.slice(1));
chrome.storage.local.set({ db, boundAt: Date.now() }).then(() => {
  document.getElementById("msg").textContent = "bound " + db;
  setTimeout(() => window.close(), 1500);
});
