#!/bin/bash
# Q7, Q4, Q8: BiDi in chrome and content scope with the real extension, the
# host-manifest directories LibreWolf searches, and a sanitize control: a
# cookie set before a clean shutdown, beside the binding in storage.local.
. /spike/lib.sh; mkxpi; nm ~/.mozilla/native-messaging-hosts; echo '[]' > ~/cmds.json
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
start() { MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $LW --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/lw.out 2>&1 & sleep 6; }
start
U=$(grep -o "mirrormark@chriscz.github.io[^,}]*" "$P/prefs.js" | grep -oE '[0-9a-f-]{36}'); echo "uuid from prefs.js: $U"
bidi 9222 chrome \
  '["XREUserNativeManifests","XRESysNativeManifests","XREMozUserNativeManifests","XREMozSysNativeManifests"].map(k=>Services.dirsvc.get(k, Ci.nsIFile).path)' \
  'PlacesUtils.bookmarks.insert({parentGuid: PlacesUtils.bookmarks.toolbarGuid, url: "https://e2e.example/", title: "from chrome ctx"}).then(b=>b.guid)' \
  '(Services.cookies.add("example.com", "/", "probe", "1", true, false, false, Math.floor(Date.now()/1000)+86400, {}, Ci.nsICookie.SAMESITE_LAX, Ci.nsICookie.SCHEME_HTTPS, false), Services.cookies.cookies.map(c=>c.host+":"+c.name+(c.isSession?" (session)":" (expires "+new Date(c.expiry*1000).toISOString().slice(0,10)+")")))'
bidi 9222 content "NAV moz-extension://$U/bind.html#e2e-db" \
  'new Promise(r=>setTimeout(r,500)).then(()=>chrome.storage.local.get(["db"]))' \
  'chrome.bookmarks.getChildren("toolbar_____").then(l=>l.map(b=>b.title))'
sleep 3; grep -o '"type": "hello", "db": "[^"]*"' ~/host.log
bidi 9222 chrome 'Services.startup.quit(Ci.nsIAppStartup.eForceQuit)' >/dev/null 2>&1; wait; sleep 1
echo "--- after a clean quit and restart"; start
bidi 9222 chrome 'Services.cookies.cookies.map(c=>c.host+":"+c.name+(c.isSession?" (session)":" (expires "+new Date(c.expiry*1000).toISOString().slice(0,10)+")"))'
bidi 9222 content "NAV moz-extension://$U/popup.html" 'chrome.storage.local.get(["db"])'
kill %1; wait
