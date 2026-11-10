package web

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(FS(), name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The page may only put request text on the screen as text nodes. A script that never uses a
// markup-parsing or navigation sink cannot turn request text into markup, a link or script.
func TestAppJSUsesNoMarkupOrNavigationSink(t *testing.T) {
	js := read(t, "app.js")
	for _, bad := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "Function(",
		"setAttribute", ".href", ".src", ".style", "srcdoc", "createContextualFragment", "DOMParser",
		"document.cookie", "localStorage", "WebSocket", "EventSource", "XMLHttpRequest", "importScripts",
		"window.open", "location.assign", "location.replace", "location.href", "postMessage",
	} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js contains %q", bad)
		}
	}
	for _, want := range []string{"textContent", "sessionStorage", "Authorization", "replaceState", "visibilitychange", "getReader"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestAppJSClearsTheFragmentBeforeTheExchange(t *testing.T) {
	js := read(t, "app.js")
	rs := strings.Index(js, "history.replaceState(null, '', '/')")
	ex := strings.Index(js, "fetch('/api/session'")
	if rs < 0 || ex < 0 || rs > ex {
		t.Fatalf("replaceState at %d must come before the exchange at %d", rs, ex)
	}
	if dc := strings.Index(js, "decodeURIComponent"); dc < rs {
		t.Fatalf("decodeURIComponent at %d runs before replaceState at %d", dc, rs)
	}
	if !strings.Contains(js, "catch (e) {\n        showLinkDead();") {
		t.Fatal("a malformed fragment must show the link-expired message")
	}
	if !strings.Contains(js, "This link was used or expired: press Enter in the queue's terminal for a new one.") {
		t.Fatal("the link-expired message of the trust-boundary doc is missing")
	}
}

// Request-derived strings must reach the DOM only through textContent: every createElement
// class argument must be a literal, never a variable.
func TestClassNamesAreLiteralsOnly(t *testing.T) {
	js := read(t, "app.js")
	for _, m := range regexp.MustCompile(`\bel\('[a-z0-9]+',\s*([^,)]+)`).FindAllStringSubmatch(js, -1) {
		arg := strings.TrimSpace(m[1])
		if !strings.HasPrefix(arg, "'") {
			t.Errorf("el() class argument is not a literal: %q", arg)
		}
	}
	if regexp.MustCompile(`className\s*=\s*[^;]*[+]`).MatchString(js) {
		t.Error("a className is built by concatenation")
	}
}

func TestPageFilesLoadNothingFromElsewhere(t *testing.T) {
	html := read(t, "index.html")
	if regexp.MustCompile(`(?i)(src|href)\s*=\s*"(https?:)?//`).MatchString(html) {
		t.Fatal("index.html loads a remote resource")
	}
	css := read(t, "app.css")
	if strings.Contains(css, "url(") || strings.Contains(css, "@import") {
		t.Fatal("app.css loads a resource")
	}
}

func TestAppJSIsValidJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	p, _ := filepath.Abs("app.js")
	if out, err := exec.Command(node, "--check", p).CombinedOutput(); err != nil {
		t.Fatalf("node --check: %v\n%s", err, out)
	}
}
