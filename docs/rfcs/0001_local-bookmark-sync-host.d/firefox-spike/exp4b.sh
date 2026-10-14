#!/bin/bash
# (a) does a user.js uuid pin clobber another add-on's uuid? (b) env/fds of host when Firefox starts the default profile with no args
. /spike/lib.sh; mkxpi
cp -r /spike/ext /tmp/other; cd /tmp/other; sed -i 's/mirrormark@chriscz.github.io/other@example.com/; s/"nativeMessaging", //' manifest.json; echo 'chrome.storage.local.get("n").then(({n})=>chrome.storage.local.set({n:(n??0)+1}))' > background.js; rm -f /tmp/other.xpi; zip -qr /tmp/other.xpi .
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /tmp/other.xpi "$P/extensions/other@example.com.xpi"; cp /spike/user-basic.js "$P/user.js"
runff "$P" 8
echo "before pin: $(grep -o 'other@example.com[^,}]*' $P/prefs.js)"
echo 'user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"11111111-2222-4333-8444-555555555555\"}");' >> "$P/user.js"
for i in 1 2; do runff "$P" 8; echo "after pinned start $i: $(grep -o 'other@example.com[^,}]*' $P/prefs.js)"; done
echo "=== normal launch, no args (default profile)"
: > ~/host.log
timeout 10 $FF --headless >/dev/null 2>&1
grep -E 'PARENT|ALLENV|argv' ~/host.log | cut -c1-900
