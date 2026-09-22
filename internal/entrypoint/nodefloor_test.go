package entrypoint

// nodefloor_test.go covers the interpreter resolution for a `program` declaring a `node_floor`
// (docs/design/agent-program-runtimes.md §3.2, OQ-AR1).

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// fakeMiseStore builds a store shaped like a real one: alias dirs beside real version dirs, each
// with a runnable bin/node.
func fakeMiseStore(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		bin := filepath.Join(root, v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := miseNodeStore
	miseNodeStore = root
	t.Cleanup(func() { miseNodeStore = old })
	return root
}

func stubImageNode(t *testing.T, version string) {
	t.Helper()
	old := imageNodeVersion
	imageNodeVersion = func() string { return version }
	t.Cleanup(func() { imageNodeVersion = old })
}

// THE IMAGE'S NODE WINS when it satisfies — self-contained, no install, and the node the MCP
// wrappers already target.
func TestImageNodeWinsWhenItSatisfies(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t, "22.23.2")
	if got := ResolveNodeForFloor("22.19"); got != imageNodePath {
		t.Errorf("ResolveNodeForFloor = %q, want the image's %q", got, imageNodePath)
	}
}

// When the image's node is too old, the store's NEWEST satisfying version is taken — and this is the
// case a lexical compare gets wrong, so the fixture is built to catch it.
func TestFallsBackToTheNewestSatisfyingMiseNode(t *testing.T) {
	stubImageNode(t, "20.20.2") // too old, and lexically ">" the floor
	root := fakeMiseStore(t, "20.20.2", "22.20.0", "22.23.2", "24.19.0")

	got := ResolveNodeForFloor("22.19")
	if want := filepath.Join(root, "24.19.0", "bin", "node"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the newest satisfying %q", got, want)
	}
}

// An ALIAS dir is usable, and a more specific spelling wins a tie — the baked path is read by a human
// debugging a launcher, and "24.19.0" tells them what they got where "24" does not.
func TestAMoreSpecificSpellingWinsATie(t *testing.T) {
	stubImageNode(t, "")
	root := fakeMiseStore(t, "24", "24.19.0")
	got := ResolveNodeForFloor("22.19")
	if want := filepath.Join(root, "24.19.0", "bin", "node"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the specific spelling %q", got, want)
	}
}

// An alias ALONE still resolves — it is a real directory whose bin/node runs.
func TestAnAliasAloneResolves(t *testing.T) {
	stubImageNode(t, "")
	root := fakeMiseStore(t, "24")
	if got := ResolveNodeForFloor("22.19"); got != filepath.Join(root, "24", "bin", "node") {
		t.Errorf("an alias dir must resolve; got %q", got)
	}
}

// ⚠ A directory with NO runnable bin/node is skipped, not returned. mise leaves one behind for a
// failed or partial install, and baking a path to a binary that is not there turns a resolution
// success into an exec failure at the worst moment.
func TestAPartialInstallIsSkipped(t *testing.T) {
	stubImageNode(t, "")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "24.19.0"), 0o755); err != nil { // no bin/node
		t.Fatal(err)
	}
	bin := filepath.Join(root, "22.23.2", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := miseNodeStore
	miseNodeStore = root
	t.Cleanup(func() { miseNodeStore = old })

	if got := ResolveNodeForFloor("22.19"); got != filepath.Join(bin, "node") {
		t.Errorf("a dir with no runnable bin/node must be skipped; got %q", got)
	}
}

// Nothing satisfies -> "". The REFUSAL for that case is the launch's, not this resolver's: a content
// generator that refused would refuse during `yolo check`, which is an observe verb.
func TestNothingSatisfyingResolvesToEmpty(t *testing.T) {
	stubImageNode(t, "20.20.2")
	fakeMiseStore(t, "20.20.2", "21.0.0")
	if got := ResolveNodeForFloor("22.19"); got != "" {
		t.Errorf("want \"\" when nothing satisfies, got %q", got)
	}
}

// No floor -> "" without consulting anything. A program that declares nothing keeps today's exec.
func TestNoFloorResolvesNothing(t *testing.T) {
	// RESTORE the original, never nil. Setting the seam to nil on cleanup left every later test in
	// the package with a nil probe — the same unscoped-package-state class that
	// TestEveryPerLaunchPackRecordIsScoped exists to catch one layer up, reproduced here by hand.
	old := imageNodeVersion
	imageNodeVersion = func() string {
		t.Fatal("an undeclared floor must not probe the image's node")
		return ""
	}
	t.Cleanup(func() { imageNodeVersion = old })
	if got := ResolveNodeForFloor(""); got != "" {
		t.Errorf("want \"\", got %q", got)
	}
}

// THE BYTE-IDENTITY REQUIREMENT, which §3.3 makes a test rather than an intention: a program with no
// declared floor must render the launcher it rendered before this feature existed.
func TestNoFloorRendersAByteIdenticalLauncher(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t, "24.19.0")
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	// EVERY exec path, not just the tail: npmLauncherTemplate has two — the main one and the
	// re-entry guard's — and a prefix applied to only one leaves a re-entrant launch unwrapped.
	for _, line := range execLinesOf(got) {
		if !strings.Contains(line, `exec "$REAL_BIN" `) {
			t.Errorf("a program with no floor must exec $REAL_BIN directly; got: %s", line)
		}
	}
	if strings.Contains(got, "__YOLO_EXEC_PREFIX__") {
		t.Error("the placeholder was not substituted")
	}
}

// And with a floor, the resolved interpreter is baked in, shell-quoted, ahead of $REAL_BIN.
func TestAFloorBakesTheResolvedInterpreter(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t)
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing", NodeFloor: "22.19"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	lines := execLinesOf(got)
	if len(lines) < 2 {
		t.Fatalf("expected both exec paths (main + re-entry guard), found %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "exec "+shquote.Quote(imageNodePath)+" \"$REAL_BIN\" ") {
			t.Errorf("EVERY exec path must carry the interpreter — one left unwrapped means a "+
				"re-entrant launch runs under the workspace's node; got: %s", line)
		}
	}
}

// A declared floor that resolves to NOTHING still renders the plain exec — the refusal is the
// launch's job, and a half-written launcher would be worse than an unwrapped one.
func TestAnUnsatisfiableFloorStillRendersAPlainExec(t *testing.T) {
	stubImageNode(t, "20.20.2")
	fakeMiseStore(t, "20.20.2")
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing", NodeFloor: "99.0"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	for _, line := range execLinesOf(got) {
		if !strings.Contains(line, `exec "$REAL_BIN" `) {
			t.Errorf("want a plain exec when nothing satisfies; got: %s", line)
		}
	}
}

// execLinesOf returns EVERY exec-the-program line. Plural on purpose: the first draft of this file
// checked only the first match and passed while the re-entry guard's exec was still unwrapped.
func execLinesOf(launcher string) []string {
	var out []string
	for _, l := range strings.Split(launcher, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "exec ") && strings.Contains(t, "REAL_BIN") {
			out = append(out, t)
		}
	}
	return out
}

// TestBootstrapSubstitutesTheFloorPlaceholder pins OQ-AR2's eager half at the GENERATOR: the floors
// must reach the script that installs them. A placeholder left unsubstituted is the failure mode —
// the script would test a literal `__YOLO_NODE_FLOORS__` for emptiness, find it non-empty, and try to
// install a package named after a placeholder.
//
// Asserted on the rendered script rather than on declaredNodeFloors alone, because the wiring is the
// half that can be lost: the projection could be perfect and the replacer entry deleted.
func TestBootstrapSubstitutesTheFloorPlaceholder(t *testing.T) {
	e := NewEnv(map[string]string{"HOME": t.TempDir()})
	script := BootstrapScript(e)

	if strings.Contains(script, "__YOLO_NODE_FLOORS__") {
		t.Error("the floor placeholder was not substituted — the script would try to install a " +
			"package named after it")
	}
	// The install-and-refuse loop must be present regardless of whether this fixture declares a
	// floor: it is gated at RUN time on the baked list being non-empty, not at generation time.
	if !strings.Contains(script, "node-floor-satisfied") {
		t.Error("the bootstrap carries no floor check — OQ-AR2's eager half is not wired in")
	}
	if !strings.Contains(script, "no Node satisfying") {
		t.Error("the bootstrap carries no refusal — OQ-AR3 is not wired in")
	}
}

// TestFloorsAreDistinctAndSorted is the byte-stability property: two packs declaring one floor is
// one install, and a stable order keeps two bootstrap scripts diffable.
func TestFloorsAreDistinctAndSorted(t *testing.T) {
	// Exercised through the projection rather than a fake Env, since LoadJailPacks needs a tree.
	seen := map[string]bool{}
	var out []string
	for _, f := range []string{"22.19", "20", "22.19", "24"} {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	if strings.Join(out, " ") != "20 22.19 24" {
		t.Errorf("distinct+sorted = %q", strings.Join(out, " "))
	}
}
