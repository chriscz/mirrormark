#!/bin/bash
# Does Brave reopen the last session's tabs at startup? (A bind.html tab
# that failed before the extension loaded came back and rebound.)
id u >/dev/null 2>&1 || useradd -m u
. /spike/lib.sh
for how in sigterm cdp-close; do
  P=/tmp/s-$how
  if [ $how = sigterm ]; then run $P 8 "https://example.invalid/$how"
  else U python3 /spike/cdp.py 6 /spike/pages.py -- $BRAVE --no-sandbox --no-first-run --user-data-dir=$P "https://example.invalid/$how"; fi
  echo "== after $how: restore_on_startup=$(jq -c '.session.restore_on_startup // "default"' $P/Default/Preferences) exit_type=$(jq -c .profile.exit_type $P/Default/Preferences)"
  U python3 /spike/cdp.py 6 /spike/pages.py -- $BRAVE --no-sandbox --no-first-run --user-data-dir=$P
done
