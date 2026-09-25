package entrypoint

// nodefloorrefusal_test.go RUNS the generated bootstrap's Node-floor block — the eager install
// and OQ-AR3's refusal (docs/reference/agent-program-runtimes.md, "The refusal") — against fake `yolo`,
// `mise` and `npm`, and pins the two halves of "what is available" the refusal quotes.
//
// Running it is the point. The only test this block had checked that the refusal's TEXT was
// present, and the refusal was an `exit 1` that the stage's wrapper degraded like any other
// failure, so the target ran anyway; a text assertion passes on exactly that script.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// floorProgram is a one-program manifest declaring a Node floor.
func floorProgram(bin, floor string) string {
	return `{"name": "x", "contributes": [{"kind": "program", "bin": "` + bin +
		`", "via": "npm", "package": "` + bin + `-pkg", "node_floor": "` + floor + `"}]}`
}

// stageFloorPacks writes each manifest to <root>/<name>/pack.json — the jail's staged layout,
// where a pack's name IS its directory's — and returns root for YOLO_PACK_ROOT.
func stageFloorPacks(t *testing.T, manifests map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// floorBootstrap is a temp jail home with a generated bootstrap and fakes for every command
// the floor block and the MCP block run.
type floorBootstrap struct {
	home, fakeBin, state, log string
	script                    string
}

func newFloorBootstrap(t *testing.T, manifests map[string]string) floorBootstrap {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH; the bootstrap is bash")
	}
	home := t.TempDir()
	b := floorBootstrap{
		home:    home,
		fakeBin: filepath.Join(home, "fake-bin"),
		state:   filepath.Join(home, "fake-state"),
		log:     filepath.Join(home, "calls.log"),
	}
	for _, d := range []string{b.fakeBin, b.state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakes := map[string]string{
		// The predicate. Its answer is the case's to choose; on "no" it prints what is
		// available, as the real one does (runNodeFloorSatisfied).
		"yolo": `#!/bin/bash
echo "yolo $*" >> "$FAKE_LOG"
[ "$1 $2" = "internal node-floor-satisfied" ] || exit 99
if [ -n "${FAKE_SATISFIED:-}" ]; then exit 0; fi
if [ -n "${FAKE_SATISFIED_AFTER_INSTALL:-}" ] && [ -e "$FAKE_STATE/installed-$3" ]; then exit 0; fi
if [ -n "${FAKE_PREDICATE_RC:-}" ]; then exit "$FAKE_PREDICATE_RC"; fi
echo "20.20.2 at /fake/store/20.20.2/bin/node"
exit 1
`,
		"mise": `#!/bin/bash
echo "mise $*" >> "$FAKE_LOG"
if [ "$1" = install ]; then
    if [ -n "${FAKE_MISE_FAIL:-}" ]; then echo "mise ERROR no version ${2#node@}" >&2; exit 1; fi
    touch "$FAKE_STATE/installed-${2#node@}"
fi
exit 0
`,
		// The MCP block's install: an UNRELATED program's step, which a refusal must not skip.
		"npm": `#!/bin/bash
echo "npm $*" >> "$FAKE_LOG"
exit 0
`,
		"fc-cache": "#!/bin/sh\nexit 0\n",
	}
	for name, body := range fakes {
		if err := os.WriteFile(filepath.Join(b.fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{
		"JAIL_HOME": home,
		// A temp workspace, never the default: the MCP block writes receipts under
		// <workspace>/.yolo, and inside a jail /workspace/.yolo is the live session's.
		"YOLO_WORKSPACE":   filepath.Join(home, "ws"),
		"YOLO_MCP_PRESETS": `["sequential-thinking"]`,
	}
	if manifests != nil {
		vars["YOLO_PACK_ROOT"] = stageFloorPacks(t, manifests)
	}
	b.script = BootstrapScript(NewEnv(vars))
	return b
}

// run executes the bootstrap with the fakes and the system dirs on PATH — never the caller's
// PATH, whose real npm-global prefix would change what the MCP block decides.
func (b floorBootstrap) run(t *testing.T, env ...string) (rc int, out, calls string) {
	t.Helper()
	path := filepath.Join(b.home, "bootstrap.sh")
	if err := os.WriteFile(path, []byte(b.script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path)
	cmd.Env = append([]string{
		"HOME=" + b.home,
		"PATH=" + b.fakeBin + ":/bin:/usr/bin",
		"FAKE_LOG=" + b.log,
		"FAKE_STATE=" + b.state,
	}, env...)
	o, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the bootstrap could not be run: %v\n%s", err, o)
	}
	c, _ := os.ReadFile(b.log)
	return rc, string(o), string(c)
}

var refusingFixture = map[string]string{"nodefloor-fixture": floorProgram("nodefloor-probe", "99")}

// OQ-AR3: nothing satisfies the floor and the install fails, so the bootstrap exits the one
// status the stage's wrapper passes through, naming the pack, the program, the floor and what
// IS available — and the MCP install above it still ran.
func TestTheBootstrapRefusesAnUnsatisfiableFloor(t *testing.T) {
	b := newFloorBootstrap(t, refusingFixture)
	rc, out, calls := b.run(t, "FAKE_MISE_FAIL=1")
	if rc != provision.RefusedStatus {
		t.Errorf("rc = %d, want provision.RefusedStatus (%d) — any other status is degraded by the "+
			"stage's wrapper and the target runs anyway", rc, provision.RefusedStatus)
	}
	for _, want := range []string{
		"REFUSING to start this jail",
		"program nodefloor-probe (pack nodefloor-fixture)", // the program and the pack
		">=99", // the floor
		"20.20.2 at /fake/store/20.20.2/bin/node", // what is available, from the predicate
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(calls, "mise install node@99") {
		t.Errorf("the bootstrap refused without trying to install the floor (OQ-AR2's eager half):\n%s", calls)
	}
	if !strings.Contains(calls, "npm install -g @modelcontextprotocol/server-sequential-thinking") {
		t.Errorf("a refused floor skipped the MCP install, which is another program's step:\n%s", calls)
	}
}

// The eager half working: nothing satisfied the floor until the stage installed one, so there is
// nothing to refuse.
func TestAFloorTheStageInstallsIsNotRefused(t *testing.T) {
	b := newFloorBootstrap(t, refusingFixture)
	rc, out, calls := b.run(t, "FAKE_SATISFIED_AFTER_INSTALL=1")
	if rc != 0 || strings.Contains(out, "REFUSING") {
		t.Errorf("rc = %d after a successful install — want 0 and no refusal:\n%s", rc, out)
	}
	if !strings.Contains(calls, "mise install node@99") {
		t.Errorf("the floor was satisfied without the install this case depends on:\n%s", calls)
	}
}

// A floor already met installs nothing — a hit is no work.
func TestASatisfiedFloorInstallsNothing(t *testing.T) {
	b := newFloorBootstrap(t, refusingFixture)
	rc, _, calls := b.run(t, "FAKE_SATISFIED=1")
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(calls, "yolo internal node-floor-satisfied 99") {
		t.Errorf("the floor was never checked — the placeholder rendered nothing:\n%s", calls)
	}
	if strings.Contains(calls, "mise install") {
		t.Errorf("a satisfied floor still ran an install:\n%s", calls)
	}
}

// A predicate that cannot answer (yolo missing, a misuse) still refuses — a broken gate must not
// open — but says it could not list what is available instead of claiming "none".
func TestAPredicateThatCannotAnswerIsNamedAsSuch(t *testing.T) {
	b := newFloorBootstrap(t, refusingFixture)
	rc, out, _ := b.run(t, "FAKE_PREDICATE_RC=127")
	if rc != provision.RefusedStatus {
		t.Errorf("rc = %d, want provision.RefusedStatus (%d)", rc, provision.RefusedStatus)
	}
	if !strings.Contains(out, "unknown (yolo internal node-floor-satisfied exited 127)") {
		t.Errorf("the refusal does not say the predicate itself failed:\n%s", out)
	}
}

// No selected pack declares a floor: the block is inert, and never asks the predicate.
func TestNoDeclaredFloorChecksNothing(t *testing.T) {
	b := newFloorBootstrap(t, map[string]string{
		"plain": `{"name": "plain", "contributes": [{"kind": "program", "bin": "pl", "via": "npm", "package": "pl"}]}`,
	})
	rc, _, calls := b.run(t, "FAKE_MISE_FAIL=1")
	if rc != 0 {
		t.Errorf("rc = %d with no floor declared, want 0", rc)
	}
	if strings.Contains(calls, "node-floor-satisfied") {
		t.Errorf("a floor was checked that nobody declared:\n%s", calls)
	}
}

// AvailableNodes lists what the resolution can see: the image's node first when readable, then
// each distinct store version once — aliases folded into the most specific spelling — newest
// first, skipping names that are not versions and directories without a runnable bin/node.
func TestAvailableNodesListsEachVersionOnceNewestFirst(t *testing.T) {
	stubImageNode(t, "24.19.0")
	root := fakeMiseStore(t, "20.20.2", "22.23.2", "24.19.0")
	for alias, target := range map[string]string{"24": "24.19.0", "22": "22.23.2", "lts": "24.19.0"} {
		if err := os.Symlink(target, filepath.Join(root, alias)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "18.0.0"), 0o755); err != nil { // a failed install
		t.Fatal(err)
	}
	got := strings.Join(AvailableNodes(), "; ")
	want := strings.Join([]string{
		"24.19.0 at " + imageNodePath,
		"24.19.0 at " + filepath.Join(root, "24.19.0", "bin", "node"),
		"22.23.2 at " + filepath.Join(root, "22.23.2", "bin", "node"),
		"20.20.2 at " + filepath.Join(root, "20.20.2", "bin", "node"),
	}, "; ")
	if got != want {
		t.Errorf("AvailableNodes =\n  %s\nwant\n  %s", got, want)
	}
}

// Nothing anywhere: the sentence says where it looked, so "none" is checkable.
func TestDescribeAvailableNodesNamesWhereItLooked(t *testing.T) {
	stubImageNode(t, "")
	root := fakeMiseStore(t)
	if got, want := DescribeAvailableNodes(),
		"none (no readable "+imageNodePath+", and no node in "+root+")"; got != want {
		t.Errorf("DescribeAvailableNodes = %q, want %q", got, want)
	}
}

// THE STORE FOLLOWS MISE_DATA_DIR. It was the constant /mise/installs/node, which is the
// container's alone: macos-user's store is <sandbox home>/.yolo/mise, so a node its stage had
// just installed was invisible to the check straight after, and once that check's failure
// refused the launch, every such stage would have refused.
func TestTheMiseStoreFollowsMiseDataDir(t *testing.T) {
	old := miseNodeStore
	miseNodeStore = ""
	t.Cleanup(func() { miseNodeStore = old })
	stubImageNode(t, "")

	data := t.TempDir()
	bin := filepath.Join(data, "installs", "node", "22.23.2", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_DATA_DIR", data)
	if got, want := ResolveNodeForFloor("22.19"), filepath.Join(bin, "node"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the node under $MISE_DATA_DIR (%q)", got, want)
	}

	// Unset (the host-side `yolo check` dry run): the container's store, as before.
	t.Setenv("MISE_DATA_DIR", "")
	if got := miseNodeStoreDir(); got != defaultMiseNodeStore {
		t.Errorf("with MISE_DATA_DIR unset the store is %q, want %q", got, defaultMiseNodeStore)
	}
}

// The status the bootstrap exits and the status the wrapper tests are one constant — rendered
// here from provision, never written out, so this pins the rendering and not a number.
func TestTheRefusalExitIsRenderedFromTheConstant(t *testing.T) {
	b := newFloorBootstrap(t, nil)
	if !strings.Contains(b.script, "    exit "+strconv.Itoa(provision.RefusedStatus)+"\n") {
		t.Errorf("the bootstrap's refusal does not exit provision.RefusedStatus (%d)", provision.RefusedStatus)
	}
}
