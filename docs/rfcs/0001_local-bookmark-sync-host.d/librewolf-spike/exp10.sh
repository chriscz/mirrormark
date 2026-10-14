#!/bin/bash
# Uninstall as uninstallFirefox does it: remove the XPI, turn the two lines
# into their resets (true, 15), restart once.
. /spike/lib.sh; mkxpi
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
runlw "$P" 12; echo "installed: $(addonstate "$P")"
rm "$P/extensions/$ID.xpi"; sed -i 's/"xpinstall.signatures.required", false/"xpinstall.signatures.required", true/; s/"extensions.autoDisableScopes", 14/"extensions.autoDisableScopes", 15/' "$P/user.js"; cat "$P/user.js"
runlw "$P" 12; echo "after uninstall + restart: $(addonstate "$P")"
grep -E 'xpinstall.signatures|autoDisableScopes|startupScanScopes' "$P/prefs.js"
