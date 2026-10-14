//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

type Browser struct {
	Name    string
	UDD     string
	cmd     *exec.Cmd
	c       *cdp
	session string // an extension page; chrome.* calls run there
	done    chan struct{}
}

func launch(t *testing.T, name, udd string, env []string) *Browser {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	logf, err := os.OpenFile(udd+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// --no-sandbox: Chrome's sandbox needs namespaces Docker does not grant.
	cmd := exec.Command("google-chrome", "--user-data-dir="+udd, "--no-sandbox", "--no-first-run",
		"--no-default-browser-check", "--disable-dev-shm-usage", "--password-store=basic",
		"--remote-debugging-pipe", "about:blank")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logf, logf
	cmd.ExtraFiles = []*os.File{inR, outW} // fds 3 and 4 in Chrome
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	b := &Browser{Name: name, UDD: udd, cmd: cmd, c: newCDP(inW, outR), done: make(chan struct{})}
	go func() {
		cmd.Wait()
		logf.Close()
		inW.Close()
		outR.Close()
		close(b.done)
	}()
	t.Cleanup(b.Close)
	if _, err := call[none, none](b.c, "", "Browser.getVersion", none{}); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

// Close shuts Chrome down through the protocol, so it writes Local State
// and Preferences as a user's quit would.
func (b *Browser) Close() {
	select {
	case <-b.done:
		return
	default:
	}
	call[none, none](b.c, "", "Browser.close", none{})
	select {
	case <-b.done:
	case <-time.After(20 * time.Second):
		b.cmd.Process.Kill()
		<-b.done
	}
}

type target struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
}

// targets lists Chrome's tabs, workers and other targets.
func (b *Browser) targets(t *testing.T) []target {
	t.Helper()
	r, err := call[none, struct {
		TargetInfos []target `json:"targetInfos"`
	}](b.c, "", "Target.getTargets", none{})
	if err != nil {
		t.Fatalf("%s: %v", b.Name, err)
	}
	return r.TargetInfos
}

// open loads the extension's popup page in a tab and attaches to it. It
// retries because on a profile's first start the extension installs after
// the first tab has loaded.
func (b *Browser) open(t *testing.T, extID string) {
	t.Helper()
	type createParams struct {
		URL string `json:"url"`
	}
	type attachParams struct {
		TargetID string `json:"targetId"`
		Flatten  bool   `json:"flatten"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tg, err := call[createParams, target](b.c, "", "Target.createTarget", createParams{URL: "chrome-extension://" + extID + "/popup.html"})
		if err != nil {
			t.Fatal(err)
		}
		at, err := call[attachParams, struct {
			SessionID string `json:"sessionId"`
		}](b.c, "", "Target.attachToTarget", attachParams{TargetID: tg.TargetID, Flatten: true})
		if err != nil {
			t.Fatal(err)
		}
		b.session = at.SessionID
		for range 20 {
			if s, err := evalJS[string](b, "typeof chrome.bookmarks"); err == nil && s == "object" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("%s: the extension page never loaded", b.Name)
}

type evalParams struct {
	Expression    string `json:"expression"`
	AwaitPromise  bool   `json:"awaitPromise"`
	ReturnByValue bool   `json:"returnByValue"`
}

type evalResult struct {
	Result struct {
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// prelude: a path starts with a root as config.yaml names it ("toolbar"
// or "other") and goes on through folders, such as "toolbar/Work/Dev". A
// node is named the way Lines prints it, below its path: "toolbar/Work/Dev/"
// is a folder, "toolbar/Work/GitHub https://github.com/" a bookmark: what
// follows the last space is a URL if it holds "://". Each
// step must match exactly one node, so two same-titled folders, or two
// identical bookmarks, are an error rather than a guess.
const prelude = `
const mm = {
  roots: { toolbar: "bookmarks-bar", other: "other" },
  one(parent, what, ok) {
    const found = (parent.children ?? []).filter(ok);
    if (found.length !== 1) {
      const there = (parent.children ?? []).map((c) => c.title + (c.url ? " " + c.url : "/"));
      throw new Error(found.length + " nodes match " + what + " among " + JSON.stringify(there));
    }
    return found[0];
  },
  async folder(path, create) {
    const [root, ...segs] = path.split("/").filter(Boolean);
    const [tree] = await chrome.bookmarks.getTree();
    let n = mm.one(tree, root, (c) => c.folderType === mm.roots[root]);
    for (const seg of segs) {
      if (create && !(n.children ?? []).some((c) => !c.url && c.title === seg)) {
        await chrome.bookmarks.create({ parentId: n.id, title: seg });
        [n] = await chrome.bookmarks.getSubTree(n.id);
      }
      n = mm.one(n, path, (c) => !c.url && c.title === seg);
    }
    return n;
  },
  async node(ref) {
    const sp = ref.lastIndexOf(" ");
    const path = ref.slice(0, sp), url = ref.slice(sp + 1);
    if (sp < 0 || !url.includes("://")) {
      if (!ref.endsWith("/")) throw new Error(ref + " is neither a folder/ nor a bookmark URL");
      return mm.folder(ref);
    }
    const i = path.lastIndexOf("/");
    const f = await mm.folder(path.slice(0, i));
    return mm.one(f, ref, (c) => c.url === url && c.title === path.slice(i + 1));
  },
  async lines(path) {
    let f;
    try {
      f = await mm.folder(path);
    } catch (e) {
      if (e.message.startsWith("0 nodes")) return null;
      throw e;
    }
    const out = [];
    const walk = (n, p) => {
      for (const c of n.children ?? []) {
        const q = p + "/" + c.title;
        out.push(c.url ? q + " " + c.url : q + "/");
        walk(c, q);
      }
    };
    walk(f, "");
    return out;
  },
};
`

func evalJS[R any](b *Browser, expr string) (R, error) {
	var out R
	res, err := call[evalParams, evalResult](b.c, b.session, "Runtime.evaluate", evalParams{
		Expression: "(async () => {" + prelude + "return await (" + expr + ");})()", AwaitPromise: true, ReturnByValue: true})
	if err != nil {
		return out, err
	}
	if d := res.ExceptionDetails; d != nil {
		return out, fmt.Errorf("%s: %s %s", expr, d.Text, d.Exception.Description)
	}
	if len(res.Result.Value) > 0 {
		err = json.Unmarshal(res.Result.Value, &out)
	}
	return out, err
}

func eval[R any](t *testing.T, b *Browser, expr string) R {
	t.Helper()
	r, err := evalJS[R](b, expr)
	if err != nil {
		t.Fatalf("%s: %v", b.Name, err)
	}
	return r
}

// q quotes s as a JavaScript string.
func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Lines lists the folder at path as "path url" lines for bookmarks and
// "path/" for folders, relative to it and sorted; nil if the folder does
// not exist. Sorted here, not in JavaScript, whose
// sort orders UTF-16 units where Go orders bytes.
func (b *Browser) Lines(t *testing.T, path string) []string {
	t.Helper()
	lines := eval[[]string](t, b, "mm.lines("+q(path)+")")
	slices.Sort(lines)
	return lines
}

// Mkdir makes the folder at path and any missing parents.
func (b *Browser) Mkdir(t *testing.T, path string) {
	t.Helper()
	eval[string](t, b, "mm.folder("+q(path)+", true).then(() => '')")
}

// Add puts a bookmark at the end of the folder at path.
func (b *Browser) Add(t *testing.T, path, title, url string) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.folder(%s).then((f) => chrome.bookmarks.create({parentId: f.id, title: %s, url: %s})).then(() => '')",
		q(path), q(title), q(url)))
}

func (b *Browser) Rename(t *testing.T, ref, title string) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => chrome.bookmarks.update(n.id, {title: %s})).then(() => '')", q(ref), q(title)))
}

func (b *Browser) SetURL(t *testing.T, ref, url string) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => chrome.bookmarks.update(n.id, {url: %s})).then(() => '')", q(ref), q(url)))
}

// Move moves the node ref names into the folder at path, to index, or to
// the end if index is negative.
func (b *Browser) Move(t *testing.T, ref, path string, index int) {
	t.Helper()
	dest := "{parentId: f.id}"
	if index >= 0 {
		dest = fmt.Sprintf("{parentId: f.id, index: %d}", index)
	}
	eval[string](t, b, fmt.Sprintf("Promise.all([mm.node(%s), mm.folder(%s)]).then(([n, f]) => chrome.bookmarks.move(n.id, %s)).then(() => '')", q(ref), q(path), dest))
}

// Remove removes the node ref names, with everything inside it.
func (b *Browser) Remove(t *testing.T, ref string) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => n.url ? chrome.bookmarks.remove(n.id) : chrome.bookmarks.removeTree(n.id)).then(() => '')", q(ref)))
}
