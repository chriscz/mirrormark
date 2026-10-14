#!/bin/bash
# e2e: drive Places from the chrome context and the extension page over BiDi with a stdlib Go client
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo '[]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-basic.js "$P/user.js"
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $FF --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/ff.out 2>&1 &
sleep 4
U=$(grep -o "mirrormark@chriscz.github.io[^,}]*" "$P/prefs.js" | grep -oE '[0-9a-f-]{36}'); echo "uuid from prefs.js: $U"
/spike/bidi/bidi 9222 chrome \
  'PlacesUtils.bookmarks.insert({parentGuid: PlacesUtils.bookmarks.toolbarGuid, url: "https://e2e.example/", title: "from chrome ctx"}).then(b=>b.guid)' \
  'WebExtensionPolicy.getByID("mirrormark@chriscz.github.io").mozExtensionHostname'
/spike/bidi/bidi 9222 content "NAV moz-extension://$U/bind.html#e2e-db" \
  'chrome.bookmarks.getChildren("toolbar_____").then(l=>l.map(b=>b.title))' \
  'chrome.storage.local.get(null)'
grep -E 'event|why' ~/host.log | cut -c1-160
kill %1; wait
