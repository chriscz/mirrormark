#!/bin/bash
cp -r /spike/ext /tmp/ext
EXTID=$(python3 -c 'import hashlib;print("".join(chr(ord("a")+int(c,16)) for c in hashlib.sha256(b"/tmp/ext").hexdigest()[:32]))')
echo id $EXTID
mkdir -p /tmp/ud/NativeMessagingHosts
echo '{"name":"com.github.chriscz.mirrormark","description":"spike","path":"/spike/host.py","type":"stdio","allowed_origins":["chrome-extension://'$EXTID'/"]}' > /tmp/ud/NativeMessagingHosts/com.github.chriscz.mirrormark.json
echo '[{"cmd":"bmtest"}]' > ~/cmds.json
timeout 15 /opt/cft/chrome-linux64/chrome --headless=new --no-sandbox --user-data-dir=/tmp/ud --load-extension=/tmp/ext --enable-logging=stderr --v=0 about:blank > /tmp/c.out 2>&1
grep -i -E 'manifest|scripts|browser_specific|warning|extension' /tmp/c.out | grep -v -i 'dbus\|gcm\|fontconfig' | head -20
cat ~/host.log | grep reply | cut -c1-3000; true || grep -m1 "RECV {\"type\": \"reply\"" ~/host.log | sed "s/^.*RECV //" | python3 -c "import json,sys; r=json.load(sys.stdin)[\"r\"]; r.pop(\"root\"); print(json.dumps(r, indent=1))"
