#!/bin/bash
# Same check as exp5's first step, for comparison: Brave, then branded
# Google Chrome (run in spike-chrome, the Chrome spike's image, with BRAVE=google-chrome COMM=chrome
# EXTDIR=/opt/google/chrome/extensions).
id u >/dev/null 2>&1 || useradd -m u
. /spike/lib.sh
mkdir -p /tmp/out; chmod 777 /tmp/out
EXTDIR=${EXTDIR:-/opt/brave.com/brave/extensions}
mkdir -p $EXTDIR; extjson >$EXTDIR/$ID.json
(echo "ID = '$ID'; TAG = '$(basename $BRAVE)'"; cat /spike/shot.py) >/tmp/shot.py
U python3 /spike/cdp.py 8 /tmp/shot.py -- $BRAVE --no-sandbox --no-first-run --user-data-dir=/tmp/r
cp /tmp/out/* /out/
