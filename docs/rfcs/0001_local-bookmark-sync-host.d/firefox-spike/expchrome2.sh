#!/bin/bash
cp -r /spike/ext /tmp/ext
/opt/cft/chrome-linux64/chrome --headless=new --no-sandbox --user-data-dir=/tmp/ud --load-extension=/tmp/ext --remote-debugging-port=9223 > /tmp/c.out 2>&1 &
sleep 5
WS=$(curl -s localhost:9223/json | python3 -c 'import json,sys; print([t for t in json.load(sys.stdin) if t["type"]=="page"][0]["webSocketDebuggerUrl"].split("9223")[1])')
/spike/bidi/bidi cdp 9223 $WS 'location.href="chrome://extensions"' >/dev/null; sleep 2
/spike/bidi/bidi cdp 9223 $WS 'chrome.developerPrivate.getExtensionsInfo().then(l=>l.filter(e=>e.name.startsWith("mirrormark")).map(e=>({warnings:e.installWarnings, manifestErrors:e.manifestErrors, runtimeErrors:e.runtimeErrors})))'
kill %1
