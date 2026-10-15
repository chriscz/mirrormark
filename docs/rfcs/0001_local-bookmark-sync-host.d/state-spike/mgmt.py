# cdp.py script: do ACTS in order in profile PROF of UDD, as the user
# would on the extensions page, and show the profile's files after each.
#   disable, enable, remove: chrome.management from the extensions page
#   bind: open bind.html and print what the tab shows
#   wait: poll the files every 0.5 s for 15 s, printing each change
import base64, subprocess
def files(): print(subprocess.run(["/spike/st.sh", UDD, PROF], capture_output=True, text=True).stdout.rstrip(), flush=True)
def page(url):
    t = call("Target.createTarget", {"url": url})["result"]["targetId"]
    s = call("Target.attachToTarget", {"targetId": t, "flatten": True})["result"]["sessionId"]
    sleep(2)
    return t, s
def ev(s, e):
    r = call("Runtime.evaluate", {"expression": e, "awaitPromise": True, "returnByValue": True, "userGesture": True}, s)
    return r.get("result", {}).get("result", {}).get("value", r)
ext_t, ext_s = page("chrome://extensions")
def mgmt(): return ev(ext_s, f"chrome.management.get('{ID}').then(x => ({{enabled: x.enabled, installType: x.installType, disabledReason: x.disabledReason, mayDisable: x.mayDisable}}), e => String(e))")
print("   management:", mgmt(), flush=True)
for a in ACTS:
    print(f"-- {a}", flush=True)
    if a in ("disable", "enable"):
        print("   ", ev(ext_s, f"chrome.management.setEnabled('{ID}', {str(a == 'enable').lower()}).then(() => 'ok', e => String(e))"))
        print("   management:", mgmt(), flush=True)
    elif a == "remove":
        # The extensions page's own Remove asks in a native dialog CDP
        # cannot answer, so the extension removes itself, as the user's
        # Remove in its context menu would, without the dialog.
        t, s = page(f"chrome-extension://{ID}/popup.html")
        ev(s, "setTimeout(() => chrome.management.uninstallSelf({showConfirmDialog: false}), 100), 'sent'")
        sleep(2)
        print("   management:", mgmt(), flush=True)
    elif a == "bind":
        t, s = page(f"chrome-extension://{ID}/bind.html#spike")
        print("   tab:", ev(s, "({url: location.href, title: document.title, text: document.body ? document.body.innerText.slice(0, 200) : null})"), flush=True)
        call("Emulation.setDeviceMetricsOverride", {"width": 900, "height": 600, "deviceScaleFactor": 1, "mobile": False}, s)
        png = call("Page.captureScreenshot", {}, s).get("result", {}).get("data")
        if png: open(f"/tmp/out/bind-{TAG}-{PROF.replace(' ', '')}-{ACTS.index(a)}.png", "wb").write(base64.b64decode(png))
        try: call("Target.closeTarget", {"targetId": t})
        except SystemExit: pass
    elif a == "wait":
        last = None
        for i in range(30):
            sleep(0.5)
            now = subprocess.run(["/spike/st.sh", UDD, PROF], capture_output=True, text=True).stdout
            if now != last: print(f"   +{(i+1)/2}s changed:", flush=True); files(); last = now
        continue
    files()
