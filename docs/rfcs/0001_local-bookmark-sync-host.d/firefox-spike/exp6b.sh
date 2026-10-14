#!/bin/bash
# new profile management (selectable profiles / profile groups)
timeout 8 /opt/devedition/firefox --headless >/dev/null 2>&1; sleep 1
P=$(ls -d ~/.config/mozilla/firefox/*.dev-edition-default)
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 /opt/devedition/firefox --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/dev/null 2>&1 &
sleep 5
/spike/bidi/bidi 9222 chrome \
 '[Services.prefs.getBoolPref("browser.profiles.enabled", null), Services.prefs.getBoolPref("browser.profiles.created", null)]' \
 '(async()=>{const {SelectableProfileService:S}=ChromeUtils.importESModule("resource:///modules/profiles/SelectableProfileService.sys.mjs"); await S.init?.(); const p = await S.createNewProfile(false); const all = await S.getAllProfiles(); return {groupDB: S.storeID, created: p.name, path: p.path, all: all.map(x=>({name:x.name,path:x.path}))}})()'
sleep 2; kill %1; wait
echo "--- profiles.ini"; cat ~/.config/mozilla/firefox/profiles.ini
ls ~/.config/mozilla/firefox ~/.config/mozilla/firefox/"Profile Groups"
for f in ~/.config/mozilla/firefox/"Profile Groups"/*.sqlite; do echo "== $f"; sqlite3 "$f" '.tables' 'select * from Profiles;' 2>&1; done
