package run

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// TestALaunchDisclosesTheArgvItRewrote is THE CALL-SITE PIN for the argv disclosure.
//
// It drives a REAL Run rather than calling noteLaunchFlagInjection, because a printer-only
// test is the shape AGENTS.md records this repo as having shipped five times: it passes
// with the production call site deleted, and what ships is then a jail that silently adds
// copilot's permission bypass to a command the user typed themselves. This one fails three
// ways — the disclosure gone from injectLaunchFlagsDisclosed, the wrapper swapped back for
// a bare packload.InjectLaunchFlags in Run, or the injection dropped from Run entirely.
//
// macos-user is the backend because it needs no container and returns from Run above the
// banner block; the injection sits above the backend dispatch, so this arm sees exactly the
// argv the container arm would.
func TestALaunchDisclosesTheArgvItRewrote(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"copilot", "chat"}
	var gotArgv []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _ []string, agentArgv []string,
		_, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		gotArgv = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	// The launch really did rewrite the argv — otherwise the disclosure assertions below
	// would pass on a launch that injected nothing, and prove nothing.
	if got := strings.Join(gotArgv, " "); got != "copilot --yolo chat" {
		t.Fatalf("the backend received %q, want %q — nothing was rewritten, so this test "+
			"cannot tell a disclosed rewrite from an absent one", got, "copilot --yolo chat")
	}

	said := stderr.String()
	for _, want := range []string{
		"CHANGED the command",   // that yolo, and not the user's shell, did this
		"copilot chat",          // before
		"copilot --yolo chat",   // after
		"added by pack copilot", // who asked for it
		"--yolo",                // and what was added
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch rewrote the user's command line without saying %q:\n%s", want, said)
		}
	}
}

// SILENT WHEN NOTHING WAS REWRITTEN, which is the ordinary launch. A line on every launch
// saying "nothing was added" is the noise that turns a disclosure surface into wallpaper,
// and the emptiness has to be decided by the injector (which knows) rather than by the
// printer guessing from a diff.
//
// Two cases in one, because they fail differently: a binary no pack declares flags for, and
// a binary that declares them where the user has already typed every one.
func TestTheArgvDisclosureIsSilentWhenNothingChanged(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	for _, argv := range [][]string{
		{"bash", "-lc", "true"},       // no declaration for this binary
		{"copilot", "--yolo", "chat"}, // declared, and already present
	} {
		var errBuf bytes.Buffer
		o := goldenOptions("/ws", t.TempDir())
		o.Stderr = &errBuf
		o.Stdout = discardBuf()
		o.injectLaunchFlagsDisclosed(packs, argv)
		if errBuf.Len() != 0 {
			t.Errorf("%v produced a disclosure but nothing was rewritten:\n%s", argv, errBuf.String())
		}
	}
}

// TestLaunchFlagInjectionHasOneDisclosedCallSite: in THIS package, packload.InjectLaunchFlags
// is reachable through injectLaunchFlagsDisclosed and nowhere else.
//
// The wrapper exists so the disclosure cannot be separated from the rewrite by moving a
// line; that is only true while it is the sole caller HERE. Same instrument, same reason, as
// TestStartLoopholesHasOneDisclosedCallSite.
//
// THE PACKAGE BOUND IS DELIBERATE, and it is not a weakening. The injector has a second
// production caller by design — entrypoint.packAliases runs it over the bare `<bin>` to build
// the jail's shell alias — and that call site carries its own disclosure, pinned by its own
// deletion test (entrypoint/shellaliasdisclosure_test.go). One producer, two mechanisms, one
// disclosure each: docs/design/declaration-parity.md §5.6. A tree-wide scan here would fail
// on that call site and the fix would be to delete the second disclosure's own pin, which is
// backwards.
func TestLaunchFlagInjectionHasOneDisclosedCallSite(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "packload.InjectLaunchFlags(") {
				continue
			}
			// The wrapper's own call is the sanctioned one.
			if name == "launchflagdisclosure.go" {
				continue
			}
			offenders = append(offenders, name+":"+itoaTest(i+1)+": "+strings.TrimSpace(line))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("packload.InjectLaunchFlags is called outside injectLaunchFlagsDisclosed, so "+
			"that path rewrites the user's command line in silence:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
