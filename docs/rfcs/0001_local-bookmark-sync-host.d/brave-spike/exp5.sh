#!/bin/bash
# Q2: what the user sees after an external install (screenshots); Q7:
# uninstall by deleting the JSON; Q6: --load-extension; session restore.
. /spike/lib.sh
mkdir -p /tmp/out; chmod 777 /tmp/out
P=/tmp/q
mkdir -p /opt/brave.com/brave/extensions; extjson >/opt/brave.com/brave/extensions/$ID.json
cdp() { (echo "ID = '$ID'; TAG = '$1'"; cat /spike/shot.py) >/tmp/shot.py; shift
  U python3 /spike/cdp.py 8 /tmp/shot.py -- $BRAVE --no-sandbox --no-first-run "$@"; }
echo "== installed"; cdp installed --user-data-dir=$P
echo "   $(extstate $P Default | tr '\n' ' ')"
echo "   restore_on_startup: $(jq -c '.session // "unset"' $P/Default/Preferences)"
echo "== JSON deleted, one restart"; rm /opt/brave.com/brave/extensions/$ID.json
cdp removed --user-data-dir=$P
echo "   $(extstate $P Default | tr '\n' ' ')"
echo "   external_uninstalls: $(jq -c '.extensions.external_uninstalls // "none"' $P/Default/Preferences)"
echo "== --load-extension, fresh data dir"
cdp loadext --user-data-dir=/tmp/le --load-extension=/home/u/.local/share/mirrormark/extension
echo "   $(extstate /tmp/le Default | tr '\n' ' ')"
grep -i 'load-extension\|not allowed' /tmp/brave.out | head -3
cp /tmp/out/* /out/ 2>/dev/null
