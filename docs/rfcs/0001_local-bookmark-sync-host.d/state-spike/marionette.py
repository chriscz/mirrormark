#!/usr/bin/python3
"""Run privileged JavaScript in a Gecko browser started with --marionette.

marionette.py SCRIPT: SCRIPT runs in the chrome context with `resolve` and
`AddonManager` in scope; what it passes to resolve() is printed. It
stands in for the user's clicks in about:addons.
"""
import json, socket, sys, time
for _ in range(60):
    try: s = socket.create_connection(("127.0.0.1", 2828)); break
    except OSError: time.sleep(0.5)
else: sys.exit("no marionette")
f = s.makefile("rb")
def recv():
    n = b""
    while (c := f.read(1)) != b":": n += c
    return json.loads(f.read(int(n)))
recv()  # hello
mid = 0
def cmd(name, params):
    global mid; mid += 1
    m = json.dumps([0, mid, name, params]).encode()
    s.sendall(str(len(m)).encode() + b":" + m)
    r = recv()
    if r[2]: sys.exit(f"{name}: {r[2]}")
    return r[3]
cmd("WebDriver:NewSession", {"capabilities": {}})
cmd("Marionette:SetContext", {"value": "chrome"})
js = ('const resolve = arguments[arguments.length - 1];'
      'const {AddonManager} = ChromeUtils.importESModule("resource://gre/modules/AddonManager.sys.mjs");'
      + sys.argv[1])
print("  ", cmd("WebDriver:ExecuteAsyncScript", {"script": js, "args": []}).get("value"), flush=True)
cmd("WebDriver:DeleteSession", {})
