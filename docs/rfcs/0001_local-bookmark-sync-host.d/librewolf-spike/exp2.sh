#!/bin/bash
# Q2, Q3, Q7: defaults and lock state of the prefs, signing, BiDi; the real
# extension dropped into a fresh profile with mirrormark's user.js
. /spike/lib.sh; mkxpi; nm ~/.mozilla/native-messaging-hosts; echo '[]' > ~/cmds.json
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha); echo "profile $P"
PREFS='["xpinstall.signatures.required","extensions.autoDisableScopes","extensions.startupScanScopes","extensions.enabledScopes","privacy.resistFingerprinting","privacy.sanitize.sanitizeOnShutdown","extensions.webextensions.restrictedDomains","remote.active-protocols","devtools.debugger.remote-enabled"]'
Q="$PREFS.map(n=>{const t=Services.prefs.getPrefType(n), g=t==32?\"getStringPref\":t==64?\"getIntPref\":t==128?\"getBoolPref\":null; const d=Services.prefs.getDefaultBranch(\"\"); return [n, g&&Services.prefs[g](n), g&&(()=>{try{return d[g](n)}catch(e){return \"(no default)\"}})(), Services.prefs.prefIsLocked(n), Services.prefs.prefHasUserValue(n)]})"
A='[Services.appinfo.name, Services.appinfo.version, AppConstants.MOZ_REQUIRE_SIGNING, AppConstants.MOZ_UPDATE_CHANNEL, AppConstants.MOZ_APP_NAME, AppConstants.MOZ_APP_REMOTINGNAME ?? null]'
ADDON='(async()=>{const {AddonManager}=ChromeUtils.importESModule("resource://gre/modules/AddonManager.sys.mjs"); const a=await AddonManager.getAddonByID("mirrormark@chriscz.github.io"); return a && {isActive:a.isActive, enabled:a.isEnabled, signed:a.signedState, appDisabled:a.appDisabled, scope:a.scope, location:a.location?.name}})()'
echo "=== defaults (no user.js), first start"
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $LW --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/lw.out 2>&1 &
sleep 8; bidi 9222 chrome "$A" "$Q"; kill %1; wait
echo "--- stderr"; grep -iv sandbox /tmp/lw.out | head -5
echo "=== drop XPI + user.js (mirrormark's three prefs), next start"
mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $LW --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/lw.out 2>&1 &
sleep 10; bidi 9222 chrome "$Q" "$ADDON" 'Services.console.getMessageArray().map(m=>m.message||(m.sourceName+": "+m.errorMessage)).filter(s=>/mirrormark|moz-extension|signature|unsigned/i.test(s)).map(s=>s.slice(0,250))'
kill %1; wait; addonstate "$P"; echo "host starts: $(grep -c START ~/host.log 2>/dev/null)"
