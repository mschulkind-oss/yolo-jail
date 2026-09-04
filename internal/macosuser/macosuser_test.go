package macosuser

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// --- Unit tests for the macOS sandbox-user helpers ------

func TestSeatbeltProfile(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", nil)
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
	// A one-level workspace needs no extra ancestor grant: /Users and
	// /Users/Shared are already literal-allowed above, and they are the whole
	// chain for /Users/Shared/proj. See TestSeatbeltGrantsWorkspaceAncestors for
	// the deeper case, which is NOT covered by those two literals.
	if contains(p, `(literal "/Users/Shared/proj")`) {
		t.Error("a one-level workspace needs no ancestor literal beyond /Users/Shared")
	}
}

// TestSeatbeltGrantsWorkspaceAncestors is the regression for a workspace nested
// deeper than /Users/Shared/<one>. The profile allowed exactly three read paths
// under the /Users deny — "/Users", "/Users/Shared", and the workspace subpath —
// and the comment above them asserted "no ancestor grant is needed". That is true
// only at depth one. For /Users/Shared/yolo/yolo-jail the INTERMEDIATE
// /Users/Shared/yolo is denied, so anything that stats the chain fails while the
// workspace itself reads fine.
//
// Measured, not hypothesized: `just format` died in a real sandbox with
//
//	fatal: Invalid path '/Users/Shared/yolo': Operation not permitted
//
// because `git ls-files` walks up looking for the repository boundary. The
// failure is nastier than a plain denial — git reports the path as INVALID, so it
// reads as a broken repo rather than a sandbox rule, and `gofmt -w` then got an
// empty file list and refused with "cannot use -w with standard input".
//
// Traversal needs (literal), not (subpath): a subpath grant on an ancestor would
// re-allow reads of every SIBLING under it — for /Users/Shared/yolo that is every
// other checkout in the same tree — which is the isolation this deny exists for.
func TestSeatbeltGrantsWorkspaceAncestors(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/yolo/yolo-jail", "", nil)
	if !contains(p, `(literal "/Users/Shared/yolo")`) {
		t.Errorf("intermediate ancestor /Users/Shared/yolo not granted; git ls-files "+
			"cannot walk up to the repo boundary:\n%s", p)
	}
	// The ancestor gets traversal only. A subpath grant here would expose every
	// sibling checkout under /Users/Shared/yolo.
	if contains(p, `(subpath "/Users/Shared/yolo")`) {
		t.Error("ancestor granted as subpath — that re-allows every sibling under it")
	}
	// The grant must still land AFTER the deny it re-allows (last match wins).
	if idx(p, `(deny file-read* (subpath "/Users"))`) >= idx(p, `(literal "/Users/Shared/yolo")`) {
		t.Error("ancestor re-allow must follow the /Users deny")
	}
	// The sandbox user's own home must not acquire ancestor literals from this:
	// /Users/_yolojail is depth one, so /Users alone already covers its chain.
	if contains(p, `(literal "/Users/_yolojail")`) {
		t.Error("sandbox home is depth-one; it needs no ancestor literal")
	}
}

// TestSeatbeltAncestorsForDeepWorkspace pins that EVERY intermediate level is
// granted, not just the parent — a three-deep workspace needs both middle links.
func TestSeatbeltAncestorsForDeepWorkspace(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/a/b/c", "", nil)
	for _, want := range []string{
		`(literal "/Users/Shared/a")`,
		`(literal "/Users/Shared/a/b")`,
		`(subpath "/Users/Shared/a/b/c")`,
	} {
		if !contains(p, want) {
			t.Errorf("deep workspace missing %q:\n%s", want, p)
		}
	}
	// The workspace itself is a subpath grant, never a bare literal — a literal
	// would allow the dir entry and deny everything inside it.
	if contains(p, `(literal "/Users/Shared/a/b/c")`) {
		t.Error("workspace must be granted as subpath, not literal")
	}
}

func TestSeatbeltEscapesPath(t *testing.T) {
	p := SeatbeltProfile(`/Users/Shared/a"b\c`, "", nil)
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
	// PATH order: blocker shims < darwin prefix < /usr/bin < lazy-installer launchers.
	var pathVal string
	for _, a := range argv {
		if len(a) > 5 && a[:5] == "PATH=" {
			pathVal = a[5:]
		}
	}
	dirs := splitColon(pathVal)
	if idxStr(dirs, "/Users/_yolojail/.yolo/bin/block") >= idxStr(dirs, "/nix/store/a-jq/bin") {
		t.Error("shims must precede darwin prefix")
	}
	if idxStr(dirs, "/nix/store/a-jq/bin") >= idxStr(dirs, "/usr/bin") {
		t.Error("darwin prefix must precede /usr/bin")
	}
	// The lazy-installer dir goes LAST, mirroring the container (see
	// entrypoint.BootPath): a launcher must only be reached when nothing else provides
	// the name, or a pack declaring a tool the system already has SHADOWS and breaks it.
	if idxStr(dirs, "/Users/_yolojail/.yolo/bin/launch") <= idxStr(dirs, "/usr/bin") {
		t.Error("the launcher dir must come AFTER /usr/bin")
	}
	if dirs[len(dirs)-1] != "/Users/_yolojail/.yolo/bin/launch" {
		t.Errorf("the launcher dir must be last on PATH, got %q", dirs[len(dirs)-1])
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

// TestStageBinaryFreshInode: the stage commands must copy-to-temp then mv (a
// fresh inode), never overwrite the staged binary in place — the macOS Mach-O
// signature-caching guard (J2 §3).
func TestStageBinaryFreshInode(t *testing.T) {
	cmds := StageBinaryCommands("/opt/yolo-jail/dist/yolo", "")
	last := cmds[len(cmds)-1]
	if last[0] != mvBin {
		t.Errorf("stage must END with mv (fresh inode), got %v", last)
	}
	// The mv target is the final staged path; the cp target is a temp (.new).
	dst := StagedYoloPath("")
	if last[len(last)-1] != dst {
		t.Errorf("mv target = %q, want staged path %q", last[len(last)-1], dst)
	}
	for _, c := range cmds {
		if c[0] == cpBin && c[len(c)-1] == dst {
			t.Errorf("cp must NOT write the final path in place (defeats fresh inode): %v", c)
		}
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
	// shQuote always wraps in single quotes and uses '\'' escaping.
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

// TestSourceLessHostFilesWireExcludesSourceBearing is the macos-user accepted
// deficiency, pinned: this backend has no bind mounts at all, so there is no
// /ctx/host-user to carry a host source into. A source-bearing entry must be
// FILTERED OUT rather than passed through to render with an empty host layer,
// which would silently serve its defaults in place of the host file the user
// named (docs/plans/host-file-staging.md, "macos-user — accepted deficiencies").
func TestSourceLessHostFilesWireExcludesSourceBearing(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("host_files", []any{
		// source-less: crosses nothing, so it is staged here.
		mapOf("path", "~/.config/seed.json", "content", "x\n"),
		// source-bearing: must not appear.
		mapOf("path", "~/.config/crosses.json", "source", "/Users/me/.config/crosses.json"),
	})

	wire := sourceLessHostFilesWire(cfg)
	if wire == "" {
		t.Fatal("source-less entry produced no wire string")
	}
	if !contains(wire, ".config/seed.json") {
		t.Errorf("wire %q is missing the source-less entry", wire)
	}
	if contains(wire, "crosses.json") {
		t.Errorf("wire %q leaked a source-bearing entry — it would render with an empty host layer", wire)
	}
}

// TestSourceLessHostFilesWireEmpty: no host_files (or only source-bearing ones)
// means no YOLO_HOST_FILES at all, so the bootstrap env is unchanged for every
// existing macos-user launch.
func TestSourceLessHostFilesWireEmpty(t *testing.T) {
	if got := sourceLessHostFilesWire(jsonx.NewOrderedMap()); got != "" {
		t.Errorf("no host_files produced wire %q, want empty", got)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("host_files", []any{
		mapOf("path", "~/.config/crosses.json", "source", "/Users/me/.config/crosses.json"),
	})
	if got := sourceLessHostFilesWire(cfg); got != "" {
		t.Errorf("only-source-bearing produced wire %q, want empty", got)
	}
}

// TestBuildRunPlanCarriesSourceLessHostFiles: the wire string must actually reach
// the bootstrap argv, which is the only channel to the darwin entrypoint.
func TestBuildRunPlanCarriesSourceLessHostFiles(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("host_files", []any{
		mapOf("path", "~/.config/seed.json", "content", "x\n"),
	})
	plan := BuildRunPlan("/Users/Shared/proj", cfg, []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", "", jsonx.NewOrderedMap(), nil, nil)

	var found bool
	for _, a := range plan.BootstrapArgv {
		if len(a) > len("YOLO_HOST_FILES=") && a[:len("YOLO_HOST_FILES=")] == "YOLO_HOST_FILES=" {
			found = true
			if !contains(a, ".config/seed.json") {
				t.Errorf("YOLO_HOST_FILES reached the argv but without the entry: %q", a)
			}
		}
	}
	if !found {
		t.Errorf("YOLO_HOST_FILES never reached the bootstrap argv: %v", plan.BootstrapArgv)
	}
}

// mapOf builds an OrderedMap from key/value pairs (config values are ordered maps).
func mapOf(pairs ...string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// TestEndpointGrantCommandsAreACLsNotAChmodWalk pins the SHAPE of the cross-uid
// grant, because the shape is the whole content of the change: the function it
// replaces (BrokerSocketGrantCommands) would have chgrp'd and chmod'd the
// endpoint file's PARENT, and under loopback-tls that parent is a directory
// holding every host service's credential for this jail. With its only plausible
// argument it group-owned /tmp and stripped the sticky bit.
//
// Nothing here can run on Linux — `chmod +a` is a macOS ACL extension — so the
// assertions are on the emitted argv, which is the whole function.
func TestEndpointGrantCommandsAreACLsNotAChmodWalk(t *testing.T) {
	const endpoint = "/private/tmp/yolo-host-services-deadbeef/claude-oauth-broker.endpoint"
	const dir = "/private/tmp/yolo-host-services-deadbeef"
	cmds := EndpointGrantCommands(endpoint, "")
	if len(cmds) != 2 {
		t.Fatalf("want exactly two ACEs (file read, dir search), got %v", cmds)
	}

	var sawFileRead, sawDirSearch bool
	for _, c := range cmds {
		if len(c) != 4 || c[1] != "+a" {
			t.Errorf("not a `chmod +a` ACE: %v", c)
			continue
		}
		if c[0] != chmodBin {
			t.Errorf("argv[0] = %q, want the pinned %q", c[0], chmodBin)
		}
		ace, target := c[2], c[3]
		// A user ACE, never a group one: SandboxGroup contains the HOST user, so a
		// group grant reaches past the single account that needs the file.
		if !hasPrefix(ace, "user:"+SandboxUser+" allow ") {
			t.Errorf("ACE %q is not a `user:%s allow` grant", ace, SandboxUser)
		}
		// Read-only, always. A socket needed write to connect(2); a file does not.
		for _, forbidden := range []string{"write", "append", "delete", "chown", "writesecurity"} {
			if contains(ace, forbidden) {
				t.Errorf("ACE %q grants %q — the sandbox only ever READS an endpoint file", ace, forbidden)
			}
		}
		switch target {
		case endpoint:
			sawFileRead = true
			if !contains(ace, "read") {
				t.Errorf("file ACE %q does not grant read", ace)
			}
			if contains(ace, "search") || contains(ace, "list") {
				t.Errorf("file ACE %q carries directory rights", ace)
			}
		case dir:
			sawDirSearch = true
			// search = traverse. NOT list: the sandbox has no business enumerating
			// the other services' endpoint files in the same directory.
			if !contains(ace, "search") {
				t.Errorf("dir ACE %q does not grant search", ace)
			}
			if contains(ace, "list") {
				t.Errorf("dir ACE %q grants list — the sandbox must not enumerate the credential dir", ace)
			}
		default:
			t.Errorf("grant targets %q, which is neither the endpoint file nor its own directory", target)
		}
		// The D4 regression, stated as an assertion: no ancestor above the per-jail
		// directory, and never a shared system directory.
		for _, shared := range []string{"/", "/tmp", "/private", "/private/tmp", "/Users"} {
			if target == shared {
				t.Errorf("grant modifies the shared system directory %q", shared)
			}
		}
		if c[0] == "chgrp" || c[0] == "chown" {
			t.Errorf("grant uses %q; ownership changes are what the ACE form replaces", c[0])
		}
	}
	if !sawFileRead || !sawDirSearch {
		t.Errorf("missing an ACE (file read=%v, dir search=%v): %v", sawFileRead, sawDirSearch, cmds)
	}

	// An explicit user overrides the default, so a future non-default sandbox
	// account does not silently get the hardcoded one.
	custom := EndpointGrantCommands(endpoint, "_other")
	if !hasPrefix(custom[0][2], "user:_other allow ") {
		t.Errorf("explicit user ignored: %q", custom[0][2])
	}
}
