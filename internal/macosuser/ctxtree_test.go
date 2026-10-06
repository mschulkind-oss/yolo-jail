package macosuser

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// DP-L1's backend half: the host bytes the run pipeline composed have to be (a) copied
// somewhere the sandbox uid can READ and cannot WRITE, (b) named to the bootstrap as
// YOLO_CTX_ROOT, and (c) reported to the jail as a real delivery, so its fail-closed
// host-layer read has a disposition to check.
//
// All three go through the PLAN, which is what the plan being a pure value buys: the
// Mac-side execution is unverifiable from Linux, and everything that DECIDES what will be
// executed is not.

const hostCtxTree = "/Users/matt/.local/share/yolo-jail/agents/proj/ctx-tree"

// deliveredCtx is a composed HostContext with one of each cell: a pack `reads-host`
// grant's /ctx destination, and a source-bearing host_files entry.
func deliveredCtx() HostContext {
	return HostContext{
		Tree:      hostCtxTree,
		Delivered: []string{packload.CtxRoot + "/host-claude/settings.json"},
		HostFiles: []config.HostFileEntry{
			{Path: ".npmrc", Source: "/Users/matt/.npmrc", Mode: config.HostFileModeReadonly},
		},
	}
}

func planWithCtx(t *testing.T, hostCtx HostContext) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{}, hostCtx,
		jsonx.NewOrderedMap(), nil, nil)
}

// The end-to-end shape at the plan level. Before DP-L1 a macos-user launch had NO context
// root anywhere in it — no stage command, no env var, no delivery — and rendered every
// `readsHost` surface from its defaults layer while reporting a successful bootstrap.
func TestRunPlanStagesAndNamesTheContextRoot(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())

	want := StagedCtxRoot(cnameFor("/Users/Shared/yolo/proj"), "")
	if plan.CtxRoot != want {
		t.Fatalf("plan.CtxRoot = %q, want %q", plan.CtxRoot, want)
	}
	// ROOT-OWNED, for a reason one step stronger than the pack root's: these bytes are
	// the human's own config, and an agent able to rewrite them composes its own next
	// launch's settings file.
	if !strings.HasPrefix(plan.CtxRoot, stateDir+"/") {
		t.Errorf("context root %q is not under the root-owned state dir %q — the sandbox "+
			"could rewrite the host bytes its own surfaces compose from", plan.CtxRoot, stateDir)
	}
	var sawCopy, sawChmod, sawMove bool
	for _, c := range plan.StageCommands {
		switch {
		case len(c) >= 4 && c[0] == cpBin && c[2] == hostCtxTree:
			sawCopy = true
		case len(c) >= 3 && c[0] == chmodBin && c[2] == "a+rX" &&
			strings.HasPrefix(c[len(c)-1], plan.CtxRoot):
			sawChmod = true
		case len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == plan.CtxRoot:
			sawMove = true
		}
	}
	if !sawCopy || !sawMove {
		t.Errorf("the context tree is never staged into %s (copy=%v move=%v): %v",
			plan.CtxRoot, sawCopy, sawMove, plan.StageCommands)
	}
	// a+rX is load-bearing: the sandbox uid is not the invoking user, so a tree left at
	// the invoking user's own modes is a tree the agent's surfaces cannot read.
	if !sawChmod {
		t.Errorf("the staged context tree is never made readable to the sandbox uid: %v",
			plan.StageCommands)
	}
	if !containsArg(plan.BootstrapArgv, "YOLO_CTX_ROOT="+plan.CtxRoot) {
		t.Errorf("YOLO_CTX_ROOT never reached the bootstrap argv: %v", plan.BootstrapArgv)
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a correct plan reports invariant violations: %v", probs)
	}
}

// The other half of the contract: a launch that carried no host bytes says so by ABSENCE.
// A YOLO_CTX_ROOT naming a directory nothing staged would read as "nothing delivered"
// anyway; the difference is that the report below would then claim `supported` for a
// delivery that never happened, and the jail's read fails CLOSED on exactly that.
func TestRunPlanWithoutHostBytesNamesNoContextRoot(t *testing.T) {
	plan := planWithCtx(t, HostContext{})

	if plan.CtxRoot != "" {
		t.Errorf("plan.CtxRoot = %q with no host tree composed, want empty", plan.CtxRoot)
	}
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_CTX_ROOT=") {
			t.Errorf("bootstrap argv names a context root nothing staged: %q", a)
		}
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a plan with no host bytes reports invariant violations: %v", probs)
	}
}

// ⚠ THE CARVE-OUT IS RETIRED, and this is the assertion that says so. macos-user reported
// its host layers `unsupported` unconditionally, which was the only thing keeping the
// jail's fail-closed read from refusing every launch on a Mac with a
// ~/.claude/settings.json. The bytes cross now, so the report has to say `supported` and
// list what crossed — otherwise the surface composes from its defaults layer in silence,
// which is the delivery failure OQ-CO10 made unrepresentable everywhere else.
func TestRunPlanReportsHostLayersDeliveredWhenATreeIsStaged(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())

	wire, ok := argvEnvValue(plan.BootstrapArgv, packload.HostLayerEnvVar)
	if !ok {
		t.Fatalf("the bootstrap argv carries no %s: %v", packload.HostLayerEnvVar, plan.BootstrapArgv)
	}
	report, parsed := packload.ParseHostLayerReport(wire)
	if !parsed {
		t.Fatalf("%s=%q is not the report shape the jail parses — it would be read as "+
			"UNKNOWN, restoring the fail-open behaviour OQ-CO10 ended", packload.HostLayerEnvVar, wire)
	}
	if report.Delivery != packload.HostLayersSupported {
		t.Errorf("delivery = %q with a context tree staged; the jail would compose every "+
			"`readsHost` surface without the human's file and say nothing", report.Delivery)
	}
	dest := packload.CtxRoot + "/host-claude/settings.json"
	if d := report.DispositionFor(dest); d != packload.HostLayerDelivered {
		t.Errorf("disposition for %s = %q, want %q — without it a wrong-path delivery "+
			"composes from defaults in silence, which is the 2026-09-05 bug",
			dest, d, packload.HostLayerDelivered)
	}
}

// And `unsupported` survives for the case it is still TRUE of: a caller that composed no
// tree. An install capture is the shipped one (capture.go passes the zero HostContext),
// and OQ-R3's rule holds there unchanged — a launch is not refused for a delivery nobody
// attempted.
func TestRunPlanStillReportsHostLayersUnsupportedWithNoTree(t *testing.T) {
	plan := planWithCtx(t, HostContext{})

	want := packload.HostLayerEnvVar + "=" + packload.HostLayersUnsupportedWire()
	if !containsArg(plan.BootstrapArgv, want) {
		t.Errorf("the bootstrap argv does not carry %q: %v", want, plan.BootstrapArgv)
	}
}

// The source-bearing half of `host_files` reaches the wire ONLY through the staged tree,
// and the source-less half still comes from the merged config. Both together, because
// either alone is a silent half-fix: a wire without the staged entry never renders the
// user's file, and a wire that LOST the source-less entries would drop a feature that
// worked before DP-L1.
func TestRunPlanCarriesBothHalvesOfHostFiles(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("host_files", []any{
		mapOf("path", "~/.config/seed.json", "content", "x\n"),
	})
	plan := BuildRunPlan("/Users/Shared/yolo/proj", cfg, []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{}, deliveredCtx(),
		jsonx.NewOrderedMap(), nil, nil)

	wire, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_HOST_FILES")
	if !ok {
		t.Fatalf("no YOLO_HOST_FILES on the bootstrap argv: %v", plan.BootstrapArgv)
	}
	entries, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatalf("the entrypoint cannot decode the wire: %v", err)
	}
	var sawSeed, sawNpmrc bool
	for _, e := range entries {
		switch e.Path {
		case ".config/seed.json":
			sawSeed = true
		case ".npmrc":
			sawNpmrc = true
			if !e.SourceBearing() {
				t.Errorf("the staged entry lost its source on the wire: %+v", e)
			}
		}
	}
	if !sawSeed {
		t.Errorf("the source-less entry fell out of the wire — a feature that worked "+
			"before the source-bearing half was added: %s", wire)
	}
	if !sawNpmrc {
		t.Errorf("the staged source-bearing entry never reached the wire, so the "+
			"bootstrap renders nothing at ~/.npmrc: %s", wire)
	}
}

// A destination declared BOTH ways resolves to the source-bearing entry, which is
// config.LoadHostFiles' own rule for the same pair. Without the dedupe the destination is
// staged twice and the last loop iteration silently decides which one the user gets.
func TestRunPlanPrefersTheStagedSourceOverASourceLessTwin(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("host_files", []any{
		mapOf("path", "~/.npmrc", "content", "from-the-workspace\n"),
	})
	plan := BuildRunPlan("/Users/Shared/yolo/proj", cfg, []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{}, deliveredCtx(),
		jsonx.NewOrderedMap(), nil, nil)

	wire, _ := argvEnvValue(plan.BootstrapArgv, "YOLO_HOST_FILES")
	entries, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got []config.HostFileEntry
	for _, e := range entries {
		if e.Path == ".npmrc" {
			got = append(got, e)
		}
	}
	if len(got) != 1 {
		t.Fatalf("~/.npmrc appears %d times in the wire; the bootstrap would stage it "+
			"twice and the last one would win, invisibly: %s", len(got), wire)
	}
	if !got[0].SourceBearing() {
		t.Errorf("the workspace's source-less twin outranked the user's own source-bearing "+
			"entry: %+v", got[0])
	}
}

// --- The invariants, by mutation -------------------------------------------------
//
// Each of these is a one-line deletion from BuildRunPlan or buildBootstrapEnv, and every
// one of them is invisible in a rendered artifact: the plan still prints, the bootstrap
// still reports success, and the agent's config file still looks correct.

func TestPlanInvariantCatchesAnUnannouncedContextRoot(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	var stripped []string
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_CTX_ROOT=") {
			continue
		}
		stripped = append(stripped, a)
	}
	plan.BootstrapArgv = stripped

	probs := strings.Join(PlanInvariants(plan), " ")
	if !strings.Contains(probs, "YOLO_CTX_ROOT") {
		t.Errorf("a plan that stages host bytes but never tells the bootstrap where they "+
			"are passed every invariant — the entrypoint would read the literal /ctx, "+
			"which does not exist on macOS: %v", probs)
	}
}

func TestPlanInvariantCatchesAnUnstagedContextRoot(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	var kept [][]string
	for _, c := range plan.StageCommands {
		if len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == plan.CtxRoot {
			continue
		}
		kept = append(kept, c)
	}
	plan.StageCommands = kept

	probs := strings.Join(PlanInvariants(plan), " ")
	if !strings.Contains(probs, "nothing stages the context tree") {
		t.Errorf("a plan naming a context root nothing copies passed every invariant: %v", probs)
	}
}

// THE TWO-FACT CHECK, both directions. `supported` with nothing staged refuses every host
// layer on the machine (the jail's read fails closed); `unsupported` with a tree staged
// silently un-delivers bytes the launch really copied. Neither is visible anywhere else.
func TestPlanInvariantCatchesAReportThatContradictsTheTree(t *testing.T) {
	claimed := planWithCtx(t, HostContext{})
	claimed.BootstrapArgv = replaceEnvArg(claimed.BootstrapArgv, packload.HostLayerEnvVar,
		`{"delivery":"supported"}`)
	if probs := strings.Join(PlanInvariants(claimed), " "); !strings.Contains(probs, "one fact") {
		t.Errorf("a plan claiming a delivery it did not make passed every invariant: %v", probs)
	}

	denied := planWithCtx(t, deliveredCtx())
	denied.BootstrapArgv = replaceEnvArg(denied.BootstrapArgv, packload.HostLayerEnvVar,
		packload.HostLayersUnsupportedWire())
	if probs := strings.Join(PlanInvariants(denied), " "); !strings.Contains(probs, "one fact") {
		t.Errorf("a plan that staged a tree and told the jail this backend cannot deliver "+
			"passed every invariant: %v", probs)
	}
}

// A malformed wire makes ConfigureHostFiles skip EVERY entry at once — the user's whole
// `host_files` key gone from one bad value — and nothing else in the plan shows it.
func TestPlanInvariantCatchesAWireTheEntrypointCannotDecode(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	plan.BootstrapArgv = replaceEnvArg(plan.BootstrapArgv, "YOLO_HOST_FILES", "not json at all")

	probs := strings.Join(PlanInvariants(plan), " ")
	if !strings.Contains(probs, "YOLO_HOST_FILES") {
		t.Errorf("a wire the entrypoint cannot parse passed every invariant: %v", probs)
	}
}

// ⚠ AND THE INVARIANT THAT MUST NOT EXIST. An earlier cut refused a plan carrying a
// source-bearing entry with no staged tree, which reads as careful and is wrong: a source
// that does not exist yet is a NORMAL state (a dotfile the user has not written), the entry
// still crosses so the destination renders from its `defaults`/`content` layers, and that
// is precisely what the container path does — it emits the entry and skips only the bind.
//
// Refusing here would make this backend answer differently in the one state nobody would
// think to test, which is the same class of divergence DP-L1 exists to close. Pinned as a
// test because the wrong version passed every other test in this file.
func TestPlanAcceptsASourceBearingEntryWhoseSourceIsAbsent(t *testing.T) {
	absent := HostContext{HostFiles: deliveredCtx().HostFiles} // entries, no tree
	plan := planWithCtx(t, absent)

	if plan.CtxRoot != "" {
		t.Fatalf("a HostContext with no Tree produced a context root %q", plan.CtxRoot)
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a launch whose only source-bearing entry has no host file yet was "+
			"REFUSED: %v\nOn every other backend that entry renders from its defaults "+
			"layer and the launch proceeds", probs)
	}
	wire, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_HOST_FILES")
	if !ok || !strings.Contains(wire, ".npmrc") {
		t.Errorf("the entry fell out of the wire, so nothing renders at ~/.npmrc at all — "+
			"the pre-DP-L1 behaviour surviving in the one case nobody looks at: %q", wire)
	}
}

func TestPlanInvariantCatchesAContextRootOutsideTheStateDir(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	plan.CtxRoot = "/Users/Shared/yolo/proj/.yolo/home/ctx"

	probs := strings.Join(PlanInvariants(plan), " ")
	if !strings.Contains(probs, "root-owned state dir") {
		t.Errorf("a context root inside the AGENT-WRITABLE workspace tier passed the "+
			"siting check; the agent could rewrite the bytes its own surfaces compose "+
			"from: %v", probs)
	}
}

// `mv src dst` moves src INSIDE dst when dst is an existing directory, so a stage that
// skipped the destination removal would bury the tree one level deeper every launch and
// the bootstrap would find an empty root from the second launch onward. Invisible on a
// first run, which is the only run a Mac-side smoke test is likely to do.
func TestStageCtxCommandsReplaceRatherThanNest(t *testing.T) {
	cmds := StageCtxCommands(hostCtxTree, "proj", "")
	dst := StagedCtxRoot("proj", "")

	removedDst, moved := -1, -1
	for i, c := range cmds {
		if len(c) >= 3 && c[0] == rmBin && c[2] == dst {
			removedDst = i
		}
		if len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == dst {
			moved = i
		}
	}
	if removedDst < 0 || moved < 0 {
		t.Fatalf("stage commands do not remove-then-move the destination: %v", cmds)
	}
	if removedDst > moved {
		t.Errorf("the destination is removed AFTER the move (%d > %d) — the tree would "+
			"nest one level deeper each launch: %v", removedDst, moved, cmds)
	}
	// Nothing it touches leaves the state dir; the two `rm -rf`s are why that is asserted
	// rather than assumed.
	for _, c := range cmds {
		for _, a := range c[1:] {
			if strings.HasPrefix(a, "/") && !strings.HasPrefix(a, stateDir+"/") && a != hostCtxTree {
				t.Errorf("stage command touches %q, outside the state dir %q: %v", a, stateDir, c)
			}
		}
	}
	if got := StageCtxCommands("", "proj", ""); got != nil {
		t.Errorf("no host tree should stage nothing, got %v", got)
	}
}

// The context tree is a SEPARATE leaf from the packs and the home overlay. One tree under
// another's root would make a `rm -rf` of the outer one take the inner with it, and would
// put the human's config bytes where LoadJailPacks walks for pack manifests.
func TestStagedCtxRootIsItsOwnLeaf(t *testing.T) {
	ctx := StagedCtxRoot("proj", "")
	for name, other := range map[string]string{
		"pack root":    StagedPackRoot("proj", ""),
		"home overlay": StagedHomeOverlay("proj", ""),
	} {
		if ctx == other || strings.HasPrefix(ctx, other+"/") || strings.HasPrefix(other, ctx+"/") {
			t.Errorf("the context root %q overlaps the %s %q", ctx, name, other)
		}
	}
}

// replaceEnvArg sets one K=V word on an argv, so a mutation test can state the WRONG value
// rather than delete the right one — the two are different failures and the invariants have
// to catch both.
//
// It INSERTS when the key is absent, into the env block `env -i` reads (everything up to
// the first word that is not a pair). A plain replace would have made
// TestPlanInvariantCatchesASourceBearingEntryWithNoTree vacuous: a plan with no host_files
// carries no YOLO_HOST_FILES word, so the "wrong" value it was supposed to assert would
// never have reached the argv and the test would have passed on an unmutated plan.
func replaceEnvArg(argv []string, key, value string) []string {
	out := make([]string, 0, len(argv)+1)
	inserted, inEnv := false, false
	for _, a := range argv {
		switch {
		case strings.HasPrefix(a, key+"="):
			out, inserted = append(out, key+"="+value), true
		case inEnv && !inserted && !strings.Contains(a, "="):
			// The command word: the env block ends here, so the pair goes in front of it.
			out, inserted = append(out, key+"="+value, a), true
		default:
			out = append(out, a)
		}
		if a == "-i" {
			inEnv = true
		}
	}
	if !inserted {
		out = append(out, key+"="+value)
	}
	return out
}

// THE DRY RUN SAYS WHICH STATE IT IS IN, both ways. "this launch carried no host bytes"
// and "host bytes cannot cross on this backend" were indistinguishable from outside for as
// long as this backend existed — that indistinguishability IS the row DP-L1 closes — so a
// plan that printed the tree only when non-empty would preserve it, and a `--dry-run` is
// the one surface a user reads to find out what a launch will do.
//
// It must name the STAGED path and not the config's /ctx half: /ctx cannot be made to
// exist on macOS without /etc/synthetic.conf and a reboot, so printing it would send a
// reader to a directory that is not there.
func TestPlanRenderNamesTheContextTreeEitherWay(t *testing.T) {
	var staged, bare strings.Builder
	PrintPlan(&staged, planWithCtx(t, deliveredCtx()), nil)
	PrintPlan(&bare, planWithCtx(t, HostContext{}), nil)

	want := StagedCtxRoot(cnameFor("/Users/Shared/yolo/proj"), "")
	if !strings.Contains(staged.String(), want) {
		t.Errorf("the plan never names the staged context tree %s:\n%s", want, staged.String())
	}
	// PrintPlan strips its markup, so the plain text is what to look for: a "[dim]none"
	// literal can never appear in its output, and a check spelled that way cannot fail.
	if strings.Contains(staged.String(), "host bytes:  none") {
		t.Errorf("the plan says no host bytes were staged while staging some:\n%s", staged.String())
	}
	if !strings.Contains(bare.String(), "host bytes:") {
		t.Errorf("a launch that carried no host bytes does not say so, which leaves it "+
			"indistinguishable from a backend that cannot carry any:\n%s", bare.String())
	}
	// A path that BEGINS at /ctx, not one containing it: the staged tree itself is
	// /var/yolo-jail/ctx/<cname>, which every plan now names as $YOLO_CONTEXT_DIR (CX-D4).
	if regexp.MustCompile(`(^|[\s=:'"])` + regexp.QuoteMeta(packload.CtxRoot+"/")).MatchString(bare.String()) {
		t.Errorf("the plan names a literal /ctx path, which cannot exist on macOS:\n%s",
			bare.String())
	}
}

// THE STAGED TREE'S ROOT IS OPENED TO THE SANDBOX ACCOUNT ALONE, through the plan: a composed
// tree holds the user's own files (a directory host_files entry is routinely ~/.aws), so the
// contents' a+rX is reachable only past a 0700 root carrying one `user:` ACE for the sandbox,
// both set after the recursive chmod (which would otherwise reopen the root) and before the swap
// (which would otherwise publish the tree open for a moment).
func TestTheStagedContextTreeIsOpenedToTheSandboxAlone(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	tmp := plan.CtxRoot + ".new"
	recursive, closed, ace, swap := -1, -1, -1, -1
	for i, c := range plan.StageCommands {
		switch {
		case len(c) == 4 && c[0] == chmodBin && c[1] == "-R" && c[2] == "a+rX" && c[3] == tmp:
			recursive = i
		case len(c) == 3 && c[0] == chmodBin && c[1] == "0700" && c[2] == tmp:
			closed = i
		case len(c) == 4 && c[0] == chmodBin && c[1] == "+a" &&
			c[2] == "user:"+SandboxUser+" allow list,search" && c[3] == tmp:
			ace = i
		case len(c) == 4 && c[0] == mvBin && c[2] == tmp && c[3] == plan.CtxRoot:
			swap = i
		}
	}
	if recursive < 0 || closed < 0 || ace < 0 || swap < 0 {
		t.Fatalf("the staged context tree is not closed to other accounts and opened to %s "+
			"(recursive=%d 0700=%d ace=%d swap=%d): %v", SandboxUser, recursive, closed, ace, swap,
			plan.StageCommands)
	}
	if !(recursive < closed && closed < ace && ace < swap) {
		t.Errorf("the root is closed out of order (recursive=%d 0700=%d ace=%d swap=%d): %v",
			recursive, closed, ace, swap, plan.StageCommands)
	}
}

// THE GLOBAL GITIGNORE IS NAMED TO THE BOOTSTRAP AT ITS STAGED PATH — the context root plus the
// reserved name, a physical path because there is no /ctx on macOS — and only when the host CLI
// copied one.
func TestRunPlanNamesTheStagedGlobalGitignoreToTheBootstrap(t *testing.T) {
	ctx := deliveredCtx()
	ctx.GlobalGitignore = paths.ContextGlobalGitignore
	plan := planWithCtx(t, ctx)
	want := StagedCtxRoot(cnameFor("/Users/Shared/yolo/proj"), "") + "/host-user/_global-gitignore"
	if !containsArg(plan.BootstrapArgv, GlobalGitignoreEnv+"="+want) {
		t.Errorf("the bootstrap is not told %s=%s: %v", GlobalGitignoreEnv, want, plan.BootstrapArgv)
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a plan naming its staged gitignore fails its invariants: %v", probs)
	}
	for _, a := range planWithCtx(t, deliveredCtx()).BootstrapArgv {
		if strings.HasPrefix(a, GlobalGitignoreEnv+"=") {
			t.Errorf("a launch that copied no gitignore names one: %q", a)
		}
	}
}

// A gitignore named anywhere but the staged tree is a file the agent could write, and every git
// in the sandbox would obey it.
func TestPlanInvariantCatchesAGlobalGitignoreOutsideTheContextRoot(t *testing.T) {
	plan := planWithCtx(t, deliveredCtx())
	plan.BootstrapArgv = replaceEnvArg(plan.BootstrapArgv, GlobalGitignoreEnv, "/Users/Shared/yolo/proj/.gitignore-x")
	probs := strings.Join(PlanInvariants(plan), "\n")
	if !strings.Contains(probs, GlobalGitignoreEnv+"=/Users/Shared/yolo/proj/.gitignore-x is not under the staged context root") {
		t.Errorf("PlanInvariants admitted a global gitignore outside the staged tree:\n%s", probs)
	}
}
