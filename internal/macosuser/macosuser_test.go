package macosuser

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// --- Unit tests for the macOS sandbox-user helpers ------

func TestSeatbeltProfile(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "")
	for _, want := range []string{
		"(allow default)",
		`(deny file-write* (subpath "/"))`,
		`(subpath "/Users/Shared/proj")`,
		`(subpath "/Users/_yolojail")`,
		`(subpath "/tmp")`,
		`(subpath "/var/folders")`,
		`(deny file-read* (subpath "/Library/Keychains"))`,
		`(deny file-read* (subpath "/Users"))`,
		`(literal "/Users")`,
		`#"^/dev/r?disk"`,
		`#"^/dev/bpf"`,
		`(deny file-read* (subpath "/Volumes"))`,
		`(allow file-read* (subpath "/Volumes/Macintosh HD"))`,
		"(allow process-info*)",
		"(allow sysctl-read)",
	} {
		if !contains(p, want) {
			t.Errorf("seatbelt missing %q", want)
		}
	}
	// deny precedes re-allow (last-match-wins ordering).
	if idx(p, `(deny file-write* (subpath "/"))`) >= idx(p, "(allow file-write*") {
		t.Error("write deny must precede re-allow")
	}
	if idx(p, `(deny file-read* (subpath "/Users"))`) >= idx(p, `(literal "/Users")`) {
		t.Error("/Users read deny must precede re-allow")
	}
	// No ancestor-metadata block.
	if contains(p, "file-read-metadata") {
		t.Error("no per-ancestor metadata block expected")
	}
}

func TestSeatbeltEscapesPath(t *testing.T) {
	p := SeatbeltProfile(`/Users/Shared/a"b\c`, "")
	if !contains(p, `\"`) || !contains(p, `\\`) {
		t.Errorf("SBPL escaping absent: %q", p)
	}
}

func TestLaunchArgv(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("HOME", "/evil")
	env.Set("USER", "root")
	env.Set("SHELL", "/x")
	env.Set("PATH", "/evil/bin")
	env.Set("OK", "1")
	argv := LaunchArgv([]string{"claude", "--x"}, "/var/yolo-jail/p.sb", env,
		"/Users/Shared/proj", "", "", []string{"/nix/store/a-jq/bin"})
	if argv[0] != "sudo" || !inSlice(argv, "--user=_yolojail") {
		t.Error("must run as sandbox via sudo")
	}
	i := idxSlice(argv, "/usr/bin/env")
	if i < 0 || argv[i+1] != "-i" {
		t.Error("env -i must follow /usr/bin/env")
	}
	if !inSlice(argv, "HOME=/Users/_yolojail") || inSlice(argv, "HOME=/evil") {
		t.Error("HOME must be protected")
	}
	if inSlice(argv, "USER=root") || inSlice(argv, "PATH=/evil/bin") {
		t.Error("USER/PATH must be protected")
	}
	if !inSlice(argv, "OK=1") {
		t.Error("non-protected env passes through")
	}
	// PATH order: shims < darwin prefix < /usr/bin.
	var pathVal string
	for _, a := range argv {
		if len(a) > 5 && a[:5] == "PATH=" {
			pathVal = a[5:]
		}
	}
	dirs := splitColon(pathVal)
	if idxStr(dirs, "/Users/_yolojail/.yolo-shims") >= idxStr(dirs, "/nix/store/a-jq/bin") {
		t.Error("shims must precede darwin prefix")
	}
	if idxStr(dirs, "/nix/store/a-jq/bin") >= idxStr(dirs, "/usr/bin") {
		t.Error("darwin prefix must precede /usr/bin")
	}
	// Inner shell is workspace-centric.
	inner := argv[len(argv)-1]
	if argv[len(argv)-3] != "/bin/zsh" || argv[len(argv)-2] != "-c" {
		t.Error("last argv triple must be /bin/zsh -c <inner>")
	}
	if !hasPrefix(inner, "cd '/Users/Shared/proj' && exec ") || !contains(inner, "'claude' '--x'") {
		t.Errorf("inner = %q", inner)
	}
}

func TestResolvePython(t *testing.T) {
	if got, _ := ResolvePython(func(string) bool { return true }); got != "/opt/homebrew/bin/python3" {
		t.Errorf("homebrew must win: %q", got)
	}
	if got, _ := ResolvePython(func(p string) bool { return p == "/usr/bin/python3" }); got != "/usr/bin/python3" {
		t.Errorf("fallback: %q", got)
	}
	if _, ok := ResolvePython(func(string) bool { return false }); ok {
		t.Error("none exist => not ok")
	}
	c := PythonCandidates()
	if c[len(c)-1] != "/usr/bin/python3" {
		t.Error("stub must be last")
	}
}

func TestNextFreeID(t *testing.T) {
	if got := NextFreeID(map[int]struct{}{600: {}, 601: {}, 603: {}}, 600); got != 602 {
		t.Errorf("= %d", got)
	}
	if got := NextFreeID(map[int]struct{}{}, 600); got != 600 {
		t.Errorf("= %d", got)
	}
}

func TestHomeContaining(t *testing.T) {
	if h, ok := HomeContaining("/Users/matt/code/proj", ""); !ok || h != "/Users/matt" {
		t.Errorf("= %q %v", h, ok)
	}
	if h, ok := HomeContaining("/Users/matt", ""); !ok || h != "/Users/matt" {
		t.Errorf("home itself = %q %v", h, ok)
	}
	if _, ok := HomeContaining("/Users/Shared/yolo/proj", ""); ok {
		t.Error("shared is neutral")
	}
	if _, ok := HomeContaining("/opt/yolo/proj", ""); ok {
		t.Error("non-/Users is neutral")
	}
}

func TestMacosLogModes(t *testing.T) {
	if MacosLogWrapperScript("bogus") != MacosLogWrapperScript("off") {
		t.Error("unknown falls back to off")
	}
	if contains(MacosLogWrapperScript("off"), "/usr/bin/log") {
		t.Error("off must not exec log")
	}
	if !contains(MacosLogWrapperScript("full"), `exec /usr/bin/log "$@"`) {
		t.Error("full passthrough")
	}
	if !contains(MacosLogWrapperScript("user"), "/usr/bin/log show") {
		t.Error("user defaults to show")
	}
}

func TestShQuoteNotShlex(t *testing.T) {
	// _sh_quote always wraps and uses '\'' escaping, unlike shlex.quote.
	if got := shQuote("abc"); got != "'abc'" {
		t.Errorf("shQuote always wraps: %q", got)
	}
	if got := shQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("= %q", got)
	}
	if got := shQuote(""); got != "''" {
		t.Errorf("empty = %q", got)
	}
}

// --- test helpers -----------------------------------------------------------

func contains(s, sub string) bool { return idx(s, sub) >= 0 }
func idx(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func inSlice(sl []string, x string) bool {
	for _, v := range sl {
		if v == x {
			return true
		}
	}
	return false
}
func idxSlice(sl []string, x string) int {
	for i, v := range sl {
		if v == x {
			return i
		}
	}
	return -1
}
func idxStr(sl []string, x string) int { return idxSlice(sl, x) }
func splitColon(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ':' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}
