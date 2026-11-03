package macosuser

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// --- Unit tests for the macOS sandbox-user helpers ------

func TestSeatbeltProfile(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", nil, HomeReadonly{})
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
	p := SeatbeltProfile("/Users/Shared/yolo/yolo-jail", "", nil, HomeReadonly{})
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
	p := SeatbeltProfile("/Users/Shared/a/b/c", "", nil, HomeReadonly{})
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
	p := SeatbeltProfile(`/Users/Shared/a"b\c`, "", nil, HomeReadonly{})
	if !contains(p, `\"`) || !contains(p, `\\`) {
		t.Errorf("SBPL escaping absent: %q", p)
	}
}

func TestLaunchArgv(t *testing.T) {
	const envFile = "/var/yolo-jail/env/yolo-ws-abcd1234.env"
	argv := LaunchArgv([]string{"claude", "--x"}, "/var/yolo-jail/p.sb", envFile,
		"/Users/Shared/proj", "", "", []string{"/nix/store/a-jq/bin"})
	if argv[0] != "/usr/bin/sudo" || !inSlice(argv, "-n") || !inSlice(argv, "--user=_yolojail") {
		t.Error("must run as sandbox via sudo")
	}
	i := idxSlice(argv, "/usr/bin/env")
	if i < 0 || argv[i+1] != "-i" {
		t.Error("env -i must follow /usr/bin/env")
	}
	if !inSlice(argv, "HOME=/Users/_yolojail") {
		t.Error("the sandbox home must be set on the argv")
	}
	// The composed environment is NOT on the argv, and the file that carries it is named
	// and read. Both halves, because either alone passes for an argv that is wrong in the
	// other way (envfile.go).
	if !inSlice(argv, SandboxEnvFileEnv+"="+envFile) {
		t.Errorf("the argv does not name the session env file:\n%v", argv)
	}
	if !SandboxArgvReadsEnvFile(envFile, argv) {
		t.Errorf("the argv never reads the session env file:\n%v", argv)
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
	// The lazy-installer dir is SECOND, immediately after the blockers and AHEAD of the
	// install prefixes, mirroring the container (entrypoint.BootPath) under B2
	// (program-delivery.md §3.5, OQ-PD12a). It used to go last, which made it unreachable
	// from its own second invocation onward — a launcher ordered after $HOME/.local/bin
	// installs into a directory that then wins the PATH lookup forever, so the update arm
	// it carries never runs again. What now keeps it from shadowing a store or system
	// binary is the generation-time collision check, not this position.
	if idxStr(dirs, "/Users/_yolojail/.yolo/bin/block") != 0 {
		t.Error("the blocker dir must be FIRST — interception must win over installation")
	}
	if idxStr(dirs, "/Users/_yolojail/.yolo/bin/launch") != 1 {
		t.Errorf("the launcher dir must be SECOND, got index %d in %v",
			idxStr(dirs, "/Users/_yolojail/.yolo/bin/launch"), dirs)
	}
	for _, prefix := range []string{
		"/Users/_yolojail/.local/bin",
		"/Users/_yolojail/.npm-global/bin",
		"/Users/_yolojail/go/bin",
	} {
		if idxStr(dirs, "/Users/_yolojail/.yolo/bin/launch") > idxStr(dirs, prefix) {
			t.Errorf("the launcher dir must precede the install prefix %s", prefix)
		}
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
	if h, ok := HomeContaining("/Users/matt/code/proj"); !ok || h != "/Users/matt" {
		t.Errorf("= %q %v", h, ok)
	}
	if h, ok := HomeContaining("/Users/matt"); !ok || h != "/Users/matt" {
		t.Errorf("home itself = %q %v", h, ok)
	}
	if _, ok := HomeContaining("/Users/Shared/yolo/proj"); ok {
		t.Error("shared is neutral")
	}
	if _, ok := HomeContaining("/opt/yolo/proj"); ok {
		t.Error("non-/Users is neutral")
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

// TestSourceLessHostFilesWireExcludesSourceBearing pins the CREDENTIAL BOUNDARY, which is
// what this function's exclusion is now about.
//
// It used to pin an accepted deficiency — a source-bearing entry was filtered out because
// no /ctx/host-user mount existed to carry its bytes. DP-L1 closed that: the bytes cross by
// copy and the entries reach the wire through hostFilesWire. What survives unchanged, and
// is the reason this function still refuses them, is that a source-bearing entry lives in
// the USER config and is unreachable from a pure read of the merged map
// (config.SourceLessHostFilesFrom). A source-bearing entry appearing here would mean this
// package had grown the user-config read that OQ-DP4 keeps in the host CLI.
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

// TestSourceLessHostFilesWireEmpty: no host_files, or only entries this pure read cannot
// see, means no wire from THIS function. The launch's actual variable is hostFilesWire's,
// which folds in the staged source-bearing entries the host CLI resolved — see
// TestRunPlanCarriesBothHalvesOfHostFiles for the composed result.
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
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil)

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

// EVERY CROSSING INTO THE SANDBOX ACCOUNT IS THE ONE RULE, AND NO STAGING STEP RUNS SUDO: the
// plan's half of OQ-MP10 (docs/design/macos-user-privilege-lifecycle.md). A crossing that leaves
// the rule's shape prompts for a password or fails under the narrowed grant; a staging argv that
// runs sudo is a root act the ruling removed.
func TestEveryPlanArgvIsTheRuleOrRunsAsTheHost(t *testing.T) {
	plan := sessionPlan(t, "0123456789abcdef")
	links := []ContextLink{{Source: "/Users/Shared/data", Dest: "/ctx/data", Dir: true}}
	plan.ContextPreflight = ContextPreflight(RunAs{}, links)
	var argvs [][]string
	argvs = append(argvs, plan.BootstrapArgv, plan.ProvisionArgv, plan.LaunchArgv, plan.JailDaemonArgv)
	for _, p := range plan.ContextPreflight {
		argvs = append(argvs, p.Argv)
	}
	for _, a := range argvs {
		if len(a) == 0 {
			continue
		}
		if a[0] != "/usr/bin/sudo" {
			t.Errorf("crossing does not start with /usr/bin/sudo: %q", a)
		}
		if p := RunAsArgvProblems("t", a); len(p) > 0 {
			t.Errorf("%v", p)
		}
	}
	for _, c := range append(append([][]string{}, plan.StageCommands...), plan.CaptureStageCommands...) {
		if len(c) > 0 && (c[0] == "sudo" || c[0] == "/usr/bin/sudo") {
			t.Errorf("a staging argv runs sudo: %q", c)
		}
	}
	if plan.StagedDir != "/Users/Shared/yolo-staged" {
		t.Errorf("StagedDir = %q", plan.StagedDir)
	}
}

// PlanInvariants CARRIES THE RULE'S SHAPE over every crossing, so a builder that drifts from
// RunAsSandboxArgv fails the plan rather than prompting, or failing under the narrowed grant, on
// a real Mac. Each case breaks one crossing of an otherwise good plan.
func TestPlanInvariantsRefuseACrossingOutsideTheRule(t *testing.T) {
	good := sessionPlan(t, "0123456789abcdef")
	good.ContextPreflight = ContextPreflight(RunAs{}, []ContextLink{{Source: "/Users/Shared/data", Dest: "/ctx/data", Dir: true}})
	for name, mutate := range map[string]func(*RunPlan){
		"a launch that may prompt": func(p *RunPlan) {
			p.LaunchArgv = append([]string{sudoBin}, p.LaunchArgv[2:]...)
		},
		"a bootstrap through sudo on PATH": func(p *RunPlan) {
			p.BootstrapArgv = append([]string{"sudo"}, p.BootstrapArgv[1:]...)
		},
		"a provisioning stage as root": func(p *RunPlan) {
			p.ProvisionArgv = append([]string{sudoBin, "-n"}, p.ProvisionArgv[3:]...)
		},
		"a bare /bin/test preflight": func(p *RunPlan) {
			p.ContextPreflight = []ContextProbe{{Access: "read",
				Argv: []string{sudoBin, "-n", "--user=" + SandboxUser, testBin, "-r", "/Users/Shared/data"}}}
		},
	} {
		p := good
		mutate(&p)
		if len(crossingArgvProblems(p)) == 0 || len(PlanInvariants(p)) == 0 {
			t.Errorf("%s: PlanInvariants accepted the plan", name)
		}
	}
	if probs := crossingArgvProblems(good); len(probs) > 0 {
		t.Errorf("a good plan's crossings fail the rule's shape:\n%s", strings.Join(probs, "\n"))
	}
}

// A WORKSPACE INSIDE THE STAGING ROOT OR THE CAPTURE ROOT is refused: both sit under /Users/Shared
// beside the shared workspace root now, and the sandbox writes its workspace.
func TestPlanInvariantsRefuseAWorkspaceInsideYolosOwnRoots(t *testing.T) {
	for _, ws := range []string{StagingRoot() + "/proj", CaptureRootDefault() + "/proj", StagingRoot()} {
		p := sessionPlan(t, "0123456789abcdef")
		p.Workspace = ws
		if !strings.Contains(strings.Join(PlanInvariants(p), "\n"), "which yolo stages into") {
			t.Errorf("a workspace at %s passed the invariants", ws)
		}
	}
	for _, ws := range []string{"/Users/Shared/yolo/proj", StagingRoot() + "-other/proj"} {
		p := sessionPlan(t, "0123456789abcdef")
		p.Workspace = ws
		if strings.Contains(strings.Join(PlanInvariants(p), "\n"), "which yolo stages into") {
			t.Errorf("a workspace at %s was refused as inside yolo's own roots", ws)
		}
	}
}

// EVERY REMOVAL OF A STAGED TREE IS UNSEALED FIRST, and every category directory gets its mode
// explicitly. A sealed (0555) directory blocks its owner's `rm -rf` too, so a removal that forgets
// fails every relaunch after the first; unsealCommands is the one spelling, and this holds every
// staging builder to it through a full plan's stage commands.
func TestEveryStagedRemovalIsUnsealedFirstAndEveryCategoryHasItsMode(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	plan.StageCommands = append(plan.StageCommands,
		StageHomeOverlayCommands("/tmp/overlay", plan.Cname, "")...)
	removals, categories := 0, 0
	for i, c := range plan.StageCommands {
		if len(c) == 3 && c[0] == rmBin && c[1] == "-rf" {
			removals++
			if i == 0 || strings.Join(plan.StageCommands[i-1], "\x00") != strings.Join(unsealCommands(c[2])[0], "\x00") {
				t.Errorf("%v is not preceded by the unseal of the same path", c)
			}
		}
		if len(c) >= 3 && c[0] == mkdirBin && c[1] == "-p" && strings.HasPrefix(c[2], plan.StagedDir+"/") &&
			!strings.Contains(c[2], ".new") {
			categories++
			next := append([]string{chmodBin, categoryDirMode}, c[2:]...)
			if i+1 >= len(plan.StageCommands) || strings.Join(plan.StageCommands[i+1], "\x00") != strings.Join(next, "\x00") {
				t.Errorf("category directory %v is not given mode %s right after it is made", c[2:], categoryDirMode)
			}
		}
	}
	if removals == 0 || categories == 0 {
		t.Fatalf("the fixture removed %d trees and made %d categories, so it checked nothing", removals, categories)
	}
}

// TestSandboxEnvPairsCarryTheHomes pins MMP-D5 to MMP-D7 on the closed env list: self mode's HOME
// is the workspace home, its mise store the machine root's (never one under the workspace HOME),
// and the guest is told the machine root, shared store and real home on protected words a
// composed layer cannot override. Account mode carries none of the three.
func TestSandboxEnvPairsCarryTheHomes(t *testing.T) {
	ws, host := "/Users/you/code/alpha", "/Users/you"
	machine := "/Users/you/.local/share/yolo-jail/macos-user"
	shared := "/Users/you/.local/share/yolo-jail/home"
	h := LaunchHomes(ModeSelf, ws, host)
	if want := (Homes{Home: ws + "/.yolo/home", Machine: machine, Shared: shared, Real: host}); h != want {
		t.Fatalf("LaunchHomes(self) = %+v, want %+v", h, want)
	}
	pairs := sandboxEnvPairs(h, "you", "/usr/bin:/bin", "")
	for _, want := range []string{
		"HOME=" + ws + "/.yolo/home",
		"MISE_DATA_DIR=" + machine + "/.yolo/mise",
		"YOLO_DARWIN_REAL_HOME=" + host,
		"YOLO_DARWIN_MACHINE_HOME=" + machine,
		"YOLO_DARWIN_SHARED_HOME=" + shared,
	} {
		if !slices.Contains(pairs, want) {
			t.Errorf("self-mode pairs lack %s: %v", want, pairs)
		}
	}
	protected := ProtectedSandboxEnvNames()
	for _, k := range []string{"YOLO_DARWIN_REAL_HOME", "YOLO_DARWIN_MACHINE_HOME", "YOLO_DARWIN_SHARED_HOME"} {
		if !slices.Contains(protected, k) {
			t.Errorf("%s is not protected: a composed env layer could aim the session elsewhere", k)
		}
	}
	if problems := SandboxArgvEnvProblems("self", append([]string{"/usr/bin/env", "-i"}, append(pairs, "/bin/true")...)); len(problems) != 0 {
		t.Errorf("the plan invariant refuses self mode's own pairs: %v", problems)
	}

	// THE ACCOUNT MODE (MMP-D25): HOME is the workspace's own home, so two workspaces share no
	// HOME, and the account's home is the machine root, the shared root and the real home at
	// once — the caller's home is never consulted.
	acct := LaunchHomes(ModeAccount, ws, host)
	if want := (Homes{Home: ws + "/.yolo/home", Machine: SandboxHome(), Shared: SandboxHome(), Real: SandboxHome()}); acct != want {
		t.Errorf("LaunchHomes(account) = %+v, want %+v", acct, want)
	}
	if other := LaunchHomes(ModeAccount, "/Users/Shared/beta", host); other.Home == acct.Home {
		t.Errorf("two workspaces got one account-mode HOME %s", acct.Home)
	}
	acctPairs := sandboxEnvPairs(acct, SandboxUser, "/usr/bin:/bin", "")
	for _, want := range []string{
		"HOME=" + ws + "/.yolo/home",
		"MISE_DATA_DIR=" + SandboxHome() + "/.yolo/mise",
		"YOLO_DARWIN_REAL_HOME=" + SandboxHome(),
		"YOLO_DARWIN_MACHINE_HOME=" + SandboxHome(),
		"YOLO_DARWIN_SHARED_HOME=" + SandboxHome(),
	} {
		if !slices.Contains(acctPairs, want) {
			t.Errorf("account-mode pairs lack %s: %v", want, acctPairs)
		}
	}
	if problems := SandboxArgvEnvProblems("account", append([]string{"/usr/bin/env", "-i"}, append(acctPairs, "/bin/true")...)); len(problems) != 0 {
		t.Errorf("the plan invariant refuses the account mode's own pairs: %v", problems)
	}
}
