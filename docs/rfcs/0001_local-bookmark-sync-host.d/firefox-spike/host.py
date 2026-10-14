#!/usr/bin/python3
import sys, json, struct, os, time
LOG = os.environ.get("HOME") + "/host.log"
def log(*a):
    with open(LOG, "a") as f: f.write(time.strftime("%H:%M:%S ") + " ".join(str(x) for x in a) + "\n")
log("START pid", os.getpid(), "argv", sys.argv, "cwd", os.getcwd(), "ppid", os.getppid())
log("ENV", {k: v for k, v in os.environ.items() if k.startswith(("MOZ", "XRE", "HOME", "DISPLAY", "FIREFOX"))})
try:
    log("PARENT cmdline", open(f"/proc/{os.getppid()}/cmdline").read().replace("\0", " "))
except Exception as e: log("noparent", e)
try:
    fds = set()
    for fd in os.listdir(f"/proc/{os.getppid()}/fd"):
        try:
            t = os.readlink(f"/proc/{os.getppid()}/fd/{fd}")
        except OSError: continue
        if t.endswith(("places.sqlite", ".parentlock", "parent.lock", "prefs.js", "storage.sqlite")): fds.add(t)
    log("PARENT FDS", sorted(fds))
except Exception as e: log("fds failed", e)
log("ALLENV", sorted(os.environ.keys()))
def send(o):
    b = json.dumps(o).encode(); sys.stdout.buffer.write(struct.pack("<I", len(b)) + b); sys.stdout.buffer.flush()
cmds = []
cf = os.environ.get("HOME") + "/cmds.json"
if os.path.exists(cf): cmds = json.load(open(cf))
n = 0
while True:
    h = sys.stdin.buffer.read(4)
    if len(h) < 4: log("EOF"); break
    (l,) = struct.unpack("<I", h)
    m = json.loads(sys.stdin.buffer.read(l))
    log("RECV", json.dumps(m))
    if m.get("type") == "hello" and os.environ.get("SILENT_PING") is None and os.path.exists(os.environ["HOME"] + "/silent"):
        import threading
        def later():
            for t in (45, 100, 160):
                time.sleep(t - (time.time() - t0))
                log("SENDING ping at", t); send({"cmd": "ping", "n": t})
        t0 = time.time(); threading.Thread(target=later, daemon=True).start()
    if m.get("type") == "hello":
        for c in cmds:
            n += 1; c = dict(c); c["n"] = n; send(c)
