# cdp.py script: list the open pages.
print("pages:", [t["url"] for t in targets() if t["type"] == "page"])
