#!/bin/bash
# policies.json route (run as root to place the file, then as user u).
. /spike/lib.sh
su u -c '. /spike/lib.sh; mkxpi; cp /tmp/spike.xpi /home/u/spike.xpi; mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json; echo "[{\"cmd\":\"ping\"}]" > ~/cmds.json'
mkdir -p $(dirname $POLPATH)
cat > $POLPATH <<J
{"policies": {
  "ExtensionSettings": {"mirrormark@chriscz.github.io": {"installation_mode": "$MODE", "install_url": "file:///home/u/spike.xpi"}}
  $POLEXTRA
}}
J
cat $POLPATH
su u -c 'FF='$FF' bash -c ". /spike/lib.sh; \$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=\$(profdir alpha); for i in 1 2; do : > ~/host.log; runff \$P 12; echo start \$i; addonstate \$P; grep -c RECV ~/host.log; grep -o \"\\\"why\\\": \\\"[^\\\"]*\\\"\" ~/host.log; done; ls \$P/extensions"'
