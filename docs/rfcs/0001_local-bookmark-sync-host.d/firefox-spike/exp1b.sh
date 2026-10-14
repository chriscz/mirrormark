#!/bin/bash
. /spike/lib.sh; mkxpi
mkdir -p $NMDIR; echo "$HOSTJSON" > $NMDIR/com.github.chriscz.mirrormark.json
echo '[{"cmd":"ping"},{"cmd":"storage"}]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1
P=$(profdir alpha)
runff "$P" 6
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"
cp /spike/${USERJSFILE:-user-basic.js} "$P/user.js"; cat "$P/user.js"
[ -n "$EXTRA_START" ] && runff "$P" 8
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $FF --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/ff.out 2>&1 &
sleep 10
/spike/bidi/bidi 9222 chrome \
 'Services.appinfo.version' '[Services.prefs.getBoolPref("xpinstall.signatures.required"), Services.prefs.getIntPref("extensions.autoDisableScopes"), AppConstants.MOZ_REQUIRE_SIGNING, AppConstants.MOZ_UPDATE_CHANNEL]' \
 'WebExtensionPolicy.getByID("mirrormark@chriscz.github.io")?.mozExtensionHostname' \
 '(async()=>{const {AddonManager}=ChromeUtils.importESModule("resource://gre/modules/AddonManager.sys.mjs"); const a=await AddonManager.getAddonByID("mirrormark@chriscz.github.io"); return a && {isActive:a.isActive, enabled:a.isEnabled, signed:a.signedState, perms:a.userPermissions, appDisabled:a.appDisabled, blocklist:a.blocklistState, scope:a.scope, temporarilyInstalled:a.temporarilyInstalled}})()' \
 'WebExtensionPolicy.getActiveExtensions().map(p=>[p.id,p.mozExtensionHostname])' \
 'Services.console.getMessageArray().map(m=>m.message||m.errorMessage).slice(-40)'
cat ~/host.log 2>/dev/null | cut -c1-600
kill %1; wait
