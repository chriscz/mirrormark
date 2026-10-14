# cdp.py script: list the extension's targets; screenshot chrome://extensions
# and the first window to /tmp/out.
import base64
print("targets:", [t["url"] for t in targets() if ID in t["url"]])
for name, url in (("extensions", "chrome://extensions"), ("window", None)):
    t = call("Target.createTarget", {"url": url})["result"]["targetId"] if url else \
        [x for x in targets() if x["type"] == "page"][0]["targetId"]
    s = call("Target.attachToTarget", {"targetId": t, "flatten": True})["result"]["sessionId"]
    sleep(2)
    call("Emulation.setDeviceMetricsOverride", {"width": 1050, "height": 1400, "deviceScaleFactor": 1, "mobile": False}, s)
    png = call("Page.captureScreenshot", {}, s)["result"]["data"]
    open(f"/tmp/out/{name}-{TAG}.png", "wb").write(base64.b64decode(png))
