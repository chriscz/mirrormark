#!/bin/bash
FF=${FF:-/opt/devedition/firefox}
ID=mirrormark@chriscz.github.io
mkdir -p ~/.mozilla/native-messaging-hosts
cat > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json <<J
{"name":"com.github.chriscz.mirrormark","description":"spike","path":"/spike/host.py","type":"stdio","allowed_extensions":["$ID"]}
J
$FF --headless -CreateProfile "alpha" 2>&1 | grep -v Sandbox
$FF --headless -CreateProfile "beta" 2>&1 | grep -v Sandbox
find ~ -name profiles.ini -o -name installs.ini | xargs -I{} sh -c 'echo == {}; cat {}'
