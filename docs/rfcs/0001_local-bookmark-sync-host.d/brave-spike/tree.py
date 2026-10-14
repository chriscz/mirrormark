# cdp.py script: the extension's service worker, the bookmark roots, and
# whether the worker is still alive 75 s later with the host's port open.
sw = lambda: [t["url"] for t in targets() if t["type"] == "service_worker" and ID in t["url"]]
print("service worker:", sw())
print("roots:", ext_eval(ID, "chrome.bookmarks.getTree().then(([r]) => r.children.map(c => ({id: c.id, title: c.title, folderType: c.folderType, syncing: c.syncing, n: (c.children||[]).length})))"))
print("root:", ext_eval(ID, "chrome.bookmarks.getTree().then(([r]) => ({id: r.id, title: r.title, keys: Object.keys(r)}))"))
print("storage:", ext_eval(ID, "chrome.storage.local.get()"))
sleep(75)
print("service worker after 75 s:", sw())
