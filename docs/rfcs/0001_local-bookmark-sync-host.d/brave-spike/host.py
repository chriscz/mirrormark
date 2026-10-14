#!/usr/bin/python3
# Minimal native host: logs its argv and every message, replies to nothing.
import sys, json, struct, os, time
def log(*a):
    with open("/tmp/host.log", "a") as f: f.write(time.strftime("%H:%M:%S ") + " ".join(str(x) for x in a) + "\n")
log("START", os.getpid(), "argv", sys.argv)
while True:
    h = sys.stdin.buffer.read(4)
    if len(h) < 4: log("EOF", os.getpid()); break
    m = json.loads(sys.stdin.buffer.read(struct.unpack("<I", h)[0]))
    log("RECV", os.getpid(), json.dumps(m)[:4000])
