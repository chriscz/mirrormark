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

// driver is how the tests reach one browser: Chrome over the DevTools
// pipe, Firefox over WebDriver BiDi.
type driver interface {
	// eval runs expr, a JavaScript expression that may be a promise, in
	// the page openPage loaded, and returns its value as JSON.
	eval(expr string) (json.RawMessage, error)
	// openPage loads url, an extension page, and makes it eval's page.
	openPage(url string) error
	// close asks the browser to quit as a user's quit would, so that it
	// saves its files.
	close()
}

type Browser struct {
	Name string
	Kind string // a key of bins
	UDD  string // Chrome's data directory, or Firefox's profile directory
	cmd  *exec.Cmd
	drv  driver
	done chan struct{}
}

type target struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
}

type createParams struct {
	URL string `json:"url"`
}

type attachParams struct {
	TargetID string `json:"targetId"`
	Flatten  bool   `json:"flatten"`
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

type chromeDriver struct {
	c       *cdp
	session string // an extension page; chrome.* calls run there
}

func (d *chromeDriver) eval(expr string) (json.RawMessage, error) {
	res, err := call[evalParams, evalResult](d.c, d.session, "Runtime.evaluate",
		evalParams{Expression: expr, AwaitPromise: true, ReturnByValue: true})
	if err != nil {
		return nil, err
	}
	if x := res.ExceptionDetails; x != nil {
		return nil, fmt.Errorf("%s %s", x.Text, x.Exception.Description)
	}
	return res.Result.Value, nil
}

func (d *chromeDriver) openPage(url string) error {
	tg, err := call[createParams, target](d.c, "", "Target.createTarget", createParams{URL: url})
	if err != nil {
		return err
	}
	at, err := call[attachParams, struct {
		SessionID string `json:"sessionId"`
	}](d.c, "", "Target.attachToTarget", attachParams{TargetID: tg.TargetID, Flatten: true})
	if err != nil {
		return err
	}
	d.session = at.SessionID
	return nil
}

func (d *chromeDriver) close() { call[none, none](d.c, "", "Browser.close", none{}) }

func launch(t *testing.T, name, kind, udd string, env []string) *Browser {
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
	// --no-sandbox: Chromium's sandbox needs namespaces Docker does not grant.
	// --enable-logging=stderr puts the extension's console messages and
	// failed connections in the log a failing test keeps.
	cmd := exec.Command(bins[kind], "--user-data-dir="+udd, "--no-sandbox", "--no-first-run",
		"--no-default-browser-check", "--disable-dev-shm-usage", "--password-store=basic",
		"--enable-logging=stderr", "--remote-debugging-pipe", "about:blank")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logf, logf
	cmd.ExtraFiles = []*os.File{inR, outW} // fds 3 and 4 in Chrome
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	c := newCDP(inW, outR)
	b := &Browser{Name: name, Kind: kind, UDD: udd, cmd: cmd, drv: &chromeDriver{c: c}, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		logf.Close()
		inW.Close()
		outR.Close()
		close(b.done)
	}()
	t.Cleanup(b.Close)
	if _, err := call[none, none](c, "", "Browser.getVersion", none{}); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

// Close shuts the browser down through its driver, so it writes its files
// (Chrome's Local State and Preferences, Firefox's prefs.js) as a user's
// quit would.
func (b *Browser) Close() {
	select {
	case <-b.done:
		return
	default:
	}
	b.drv.close()
	select {
	case <-b.done:
	case <-time.After(20 * time.Second):
		b.cmd.Process.Kill()
		<-b.done
	}
}

// open loads the extension page url in a tab and makes it the page eval
// runs in. It retries because on a profile's first start the extension
// installs after the first tab has loaded.
func (b *Browser) open(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := b.drv.openPage(url); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			if s, err := evalJS[string](b, "typeof chrome.bookmarks"); err == nil && s == "object" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("%s: the extension page never loaded", b.Name)
}

// prelude: a path starts with a root as config.yaml names it ("toolbar"
// or "other") and goes on through folders, such as "toolbar/Work/Dev". A
// node is named the way Lines prints it, below its path: "toolbar/Work/Dev/"
// is a folder, "toolbar/Work/GitHub https://github.com/" a bookmark: what
// follows the last space is a URL if it holds "://". Each
// step must match exactly one node, so two same-titled folders, or two
// identical bookmarks, are an error rather than a guess. Paths and Lines
// skip Firefox's separators; Separators lists them.
const prelude = `
const mm = {
  roots: { toolbar: ["bookmarks-bar", "toolbar_____"], other: ["other", "unfiled_____"] },
  kids: (n) => (n.children ?? []).filter((c) => c.type !== "separator"),
  one(parent, what, ok) {
    const found = mm.kids(parent).filter(ok);
    if (found.length !== 1) {
      const there = mm.kids(parent).map((c) => c.title + (c.url ? " " + c.url : "/"));
      throw new Error(found.length + " nodes match " + what + " among " + JSON.stringify(there));
    }
    return found[0];
  },
  async folder(path, create) {
    const [root, ...segs] = path.split("/").filter(Boolean);
    const [tree] = await chrome.bookmarks.getTree();
    let n = mm.one(tree, root, (c) => (c.folderType ? c.folderType === mm.roots[root][0] : c.id === mm.roots[root][1]));
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
      for (const c of mm.kids(n)) {
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
	raw, err := b.drv.eval("(async () => {" + prelude + "return await (" + expr + ");})()")
	if err != nil {
		return out, fmt.Errorf("%s: %w", expr, err)
	}
	if len(raw) > 0 {
		err = json.Unmarshal(raw, &out)
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

// AddSeparator puts a separator at index in the folder at path, or at the
// end if index is negative (Firefox).
func (b *Browser) AddSeparator(t *testing.T, path string, index int) {
	t.Helper()
	at := ""
	if index >= 0 {
		at = fmt.Sprintf(", index: %d", index)
	}
	eval[string](t, b, fmt.Sprintf("mm.folder(%s).then((f) => chrome.bookmarks.create({parentId: f.id, type: 'separator'%s})).then(() => '')", q(path), at))
}

// Separators lists the positions of the separators directly in the folder
// at path.
func (b *Browser) Separators(t *testing.T, path string) []int {
	t.Helper()
	return eval[[]int](t, b, "mm.folder("+q(path)+").then((f) => f.children.flatMap((c, i) => c.type === 'separator' ? [i] : []))")
}

// Remove removes the node ref names, with everything inside it.
func (b *Browser) Remove(t *testing.T, ref string) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.node(%s).then((n) => n.url ? chrome.bookmarks.remove(n.id) : chrome.bookmarks.removeTree(n.id)).then(() => '')", q(ref)))
}
