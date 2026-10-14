#!/bin/bash
# Q2: is startupScanScopes=1 still needed for a profile that has run before?
. /spike/lib.sh; mkxpi
for SCAN in 1 0; do
  rm -rf ~/.config/librewolf; $LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
  runlw "$P" 8; mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"
  grep -v startupScanScopes /spike/user-mm.js > "$P/user.js"; [ $SCAN = 1 ] && grep startupScanScopes /spike/user-mm.js >> "$P/user.js"
  runlw "$P" 12; echo "existing profile, startupScanScopes line=$SCAN: $(addonstate "$P")"
done
