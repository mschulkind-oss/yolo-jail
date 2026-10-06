package cli

// sealnarrowing_test.go pins the build act's half of PPX-D39 (docs/design/patched-extensions.md):
// a build jail that stops before its build line ran is relayed with the lines it printed last — its
// own refusal — at a launch's tree arm and at `yolo capture <pack>/<name>`, and blames no runtime;
// and a patched extension's seal carries the configured base of every fork its pack declares,
// without which the narrowed selection is refused. The run pipeline's half, the gates a narrowed
// selection skips, is pinned in run's sealnarrowing_test.go.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// sealRefusal is the line the fake build jail refuses with: the refusal the first launch with
// patched extensions met, as the run pipeline prints it on its stdout.
const sealRefusal = "packs: pack treepack: briefing `agents` names \"pi\", which no pack in `packs` provides"

// refusingBuildJail is a build jail that prints what a launch prints before its staging, then the
// refusal, and exits 1 without writing the toolchain record: its build line never ran.
func refusingBuildJail(o run.Options) int {
	fmt.Fprintln(o.Stderr, "Flake source: /nix/fixture (YOLO_REPO_ROOT)")
	fmt.Fprintln(o.Stdout, sealRefusal)
	return 1
}

// A launch's tree arm relays the refusal in the cause it hands the launch, which says it once
// (run's missingbuilds.go), and its build's result says the jail stopped; nothing says a runtime or
// doubled parentheses. Red if the build act stops teeing the jail's writers or stops relaying what
// it kept.
func TestATreeBuildJailThatRefusedIsRelayedWithItsRefusal(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	withFakeCaptureJail(t, refusingBuildJail)
	d, out := fx.deliver(t, true)
	if d.Dir != "" {
		t.Fatalf("a jail that never ran its build line delivered %+v\n%s", d, out)
	}
	if !strings.Contains(out, "Building extension "+treeKeyCLI+": its build jail exited 1 before its build line ran") {
		t.Errorf("the build's result does not say its jail stopped:\n%s", out)
	}
	if d.Cause == nil || !slices.Equal(d.Cause.Lines, []string{sealRefusal}) {
		t.Errorf("the cause the launch is handed is %+v, want the refusal", d.Cause)
	}
	for _, w := range []string{"runtime", "did not start", "((", "))"} {
		if strings.Contains(out, w) || strings.Contains(d.Reason, w) {
			t.Errorf("the lines or the reason still say %q:\n%s\nreason: %s", w, out, d.Reason)
		}
	}
	if d.Cause != nil && slices.ContainsFunc(d.Cause.Lines, func(l string) bool { return strings.Contains(l, "Flake source") }) {
		t.Errorf("the relay took a line the jail printed before its refusal: %q", d.Cause.Lines)
	}
}

// A REFUSAL OF SEVERAL LINES is relayed whole, from the stream it was printed on: the config gate's
// verdict, the key it names and its `yolo check` line, where the last line alone would name the
// remedy and not the fault; and not the line the other stream printed after it.
func TestATreeBuildJailsSeveralLineRefusalIsRelayedFromItsStream(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	withFakeCaptureJail(t, func(o run.Options) int {
		fmt.Fprintln(o.Stdout, "Invalid jail config:")
		fmt.Fprintln(o.Stdout, "  • ~/.config/yolo-jail/config.jsonc:2:22: config.network.mode: expected 'bridge' or 'host'")
		fmt.Fprintln(o.Stdout, "")
		fmt.Fprintln(o.Stdout, "\x1b[2mRun `yolo check` for a full preflight before restarting.\x1b[0m")
		return 1
	})
	d, out := fx.deliver(t, true)
	want := []string{"Invalid jail config:",
		"• ~/.config/yolo-jail/config.jsonc:2:22: config.network.mode: expected 'bridge' or 'host'",
		"Run `yolo check` for a full preflight before restarting."}
	if d.Cause == nil || !slices.Equal(d.Cause.Lines, want) {
		t.Errorf("the refusal is not relayed whole, a line each (%q):\n%s\ncause: %+v", want, out, d.Cause)
	}
}

// `yolo capture <pack>/<name>` stops with the same relay, as its last line, which names the step.
func TestCaptureOfATreeWhoseJailRefusedRelaysTheRefusal(t *testing.T) {
	newTreeFixture(t, `"f.txt"`)
	withFakeCaptureJail(t, refusingBuildJail)
	var out, errw bytes.Buffer
	rc, handled := captureTree(treeKeyCLI, &out, &errw, false)
	if !handled || rc == 0 {
		t.Fatalf("captureTree = %d, %v\n%s%s", rc, handled, out.String(), errw.String())
	}
	all := out.String() + errw.String()
	for _, want := range []string{"⚠ extension " + treeKeyCLI + ": its build jail exited 1 before its build line ran",
		"\n    " + sealRefusal + "\n", "yolo capture: extension " + treeKeyCLI + ": its build jail refused to start"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the capture's stop does not relay the refusal (%q):\n%s", want, all)
		}
	}
	if !strings.Contains(all, "Fix what it names, then `yolo capture "+treeKeyCLI+"` builds it") {
		t.Errorf("the capture names no step that follows from the refusal:\n%s", all)
	}
	if strings.Contains(all, "runtime starts jails again") {
		t.Errorf("the capture blames the runtime for the jail's own refusal:\n%s", all)
	}
}

// A PATCHED EXTENSION'S SEAL CARRIES ITS PACK'S FORK BASES: the contributing pack also forks a
// configured base's program, so a selection narrowed to that pack alone is one the fork rewrite
// refuses (packload.ApplyForks: the base "is not in this selection"), on the host and in the jail.
// Red if sealPacks stops adding Fork.PackBases, or PatchedTrees stops filling it.
func TestATreesSealCarriesTheConfiguredBaseOfItsPacksFork(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main","patches":"patches",`+
		`"build":"true","produces":["f.txt"]},`+
		`{"kind":"program","bin":"tool","via":"source","fork_of":"basepack","source":"git+file://`+fx.repo+`?ref=main",`+
		`"build":"make","produces":[".local/bin/tool"]}]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+fx.treeDir+`","name":"treepack"}]}`)
	d, out := fx.deliver(t, true)
	if d.Dir == "" {
		t.Fatalf("no copy was delivered: %+v\n%s", d, out)
	}
	if len(fx.seen) != 1 || !slices.Equal(fx.seen[0].OnlyPacks, []string{"treepack", "basepack"}) {
		t.Fatalf("the build jail ran %d times, the first sealed to %v; want the contributing pack and its fork's "+
			"base", len(fx.seen), fx.seen[0].OnlyPacks)
	}
	if got := forkBuildChildArgv("/staging", forkBuild{Fork: fx.tree(t)}, false); !slices.Contains(got, "--only=basepack") {
		t.Errorf("the child build jail's argv %q does not carry the fork's base", got)
	}
}

// THE SEAL CARRIES THE BASES' OWN BASES TOO: the contributing pack forks basepack's program, and
// basepack itself forks cpack's, so a selection without cpack is one the fork rewrite refuses for
// basepack's fork ("basepack forks pack cpack's "c", and cpack is not in this selection"), as the
// user's own launch of all three is not. Red if PackBases stops at the contributing pack's own
// bases.
func TestATreesSealCarriesItsForkBasesOwnBases(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	writeFile(t, filepath.Join(fx.packs, "cpack", "pack.json"),
		`{"name":"cpack","contributes":[{"kind":"program","bin":"c","via":"npm","package":"c"}]}`)
	writeFile(t, filepath.Join(fx.packs, "basepack", "pack.json"), `{"name":"basepack","contributes":[`+
		`{"kind":"program","bin":"tool","via":"npm","package":"tool"},`+
		`{"kind":"program","bin":"c","via":"source","fork_of":"cpack","source":"git+file://`+fx.repo+`?ref=main",`+
		`"build":"make","produces":[".local/bin/c"]}]}`)
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main","patches":"patches",`+
		`"build":"true","produces":["f.txt"]},`+
		`{"kind":"program","bin":"tool","via":"source","fork_of":"basepack","source":"git+file://`+fx.repo+`?ref=main",`+
		`"build":"make","produces":[".local/bin/tool"]}]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "cpack")+`","name":"cpack"},`+
		`{"source":"file://`+filepath.Join(fx.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+fx.treeDir+`","name":"treepack"}]}`)
	d, out := fx.deliver(t, true)
	if d.Dir == "" {
		t.Fatalf("no copy was delivered: %+v\n%s", d, out)
	}
	if want := []string{"treepack", "basepack", "cpack"}; len(fx.seen) != 1 || !slices.Equal(fx.seen[0].OnlyPacks, want) {
		t.Fatalf("the build jail ran %d times, the first sealed to %v; want %v", len(fx.seen), fx.seen[0].OnlyPacks, want)
	}
	if got := forkBuildChildArgv("/staging", forkBuild{Fork: fx.tree(t)}, false); !slices.Contains(got, "--only=cpack") {
		t.Errorf("the child build jail's argv %q does not carry the base's own base", got)
	}
}
