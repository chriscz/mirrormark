#!/bin/bash
# Q2: which external extensions directory does Brave read <id>.json from?
# One candidate at a time, each with a fresh data dir.
. /spike/lib.sh
echo "id $ID version $VER"
i=0
for dir in /opt/brave.com/brave/extensions /usr/share/brave/extensions /usr/share/brave-browser/extensions \
    /usr/share/BraveSoftware/extensions /usr/share/BraveSoftware/Brave-Browser/extensions \
    /usr/share/chromium/extensions /usr/share/google-chrome/extensions /opt/google/chrome/extensions; do
  i=$((i+1)); mkdir -p "$dir"; extjson >"$dir/$ID.json"
  run /tmp/ud$i 12
  echo "== $dir: $(extstate /tmp/ud$i Default | tr '\n' ' ')"
  rm "$dir/$ID.json"
done
