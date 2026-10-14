#!/usr/bin/python3
"""Drive Brave over --remote-debugging-pipe (fds 3 and 4, NUL-ended JSON).

cdp.py SECONDS SCRIPT -- BRAVE ARGS...
SCRIPT is Python run after launch with `call(method, params, session)`,
`targets()`, `ext_eval(ext_id, expr)` and `sleep` in scope; Brave is
closed with Browser.close after it.
"""
import json, os, subprocess, sys, time
secs, script, args = float(sys.argv[1]), sys.argv[2], sys.argv[4:]
r_in, w_in = os.pipe()   # we write, Brave reads on fd 3
r_out, w_out = os.pipe() # Brave writes on fd 4, we read
def fds():
    os.dup2(r_in, 3); os.dup2(w_out, 4)
p = subprocess.Popen(args + ["--remote-debugging-pipe"], preexec_fn=fds, pass_fds=(3, 4),
                     stdout=open("/tmp/brave.out", "ab"), stderr=subprocess.STDOUT)
os.close(r_in); os.close(w_out)
rf = os.fdopen(r_out, "rb"); buf = b""; n = 0
def call(method, params=None, session=None):
    global buf, n
    n += 1; m = {"id": n, "method": method, "params": params or {}}
    if session: m["sessionId"] = session
    os.write(w_in, json.dumps(m).encode() + b"\0")
    while True:
        while b"\0" not in buf:
            c = os.read(r_out, 65536)
            if not c: raise SystemExit("pipe closed")
            buf += c
        line, buf = buf.split(b"\0", 1)
        r = json.loads(line)
        if r.get("id") == n: return r
def targets(): return call("Target.getTargets")["result"]["targetInfos"]
def ext_eval(ext_id, expr):
    t = call("Target.createTarget", {"url": f"chrome-extension://{ext_id}/popup.html"})["result"]["targetId"]
    s = call("Target.attachToTarget", {"targetId": t, "flatten": True})["result"]["sessionId"]
    time.sleep(1)
    r = call("Runtime.evaluate", {"expression": expr, "awaitPromise": True, "returnByValue": True}, s)
    call("Target.closeTarget", {"targetId": t})
    return r.get("result", {}).get("result", {}).get("value", r)
sleep = time.sleep
print("browser", call("Browser.getVersion")["result"]["product"], flush=True)
time.sleep(secs)
exec(open(script).read())
call("Browser.close")
p.wait(timeout=20)
