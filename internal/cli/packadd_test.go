package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// runPackCapture drives the same entry the CLI does (runPack's packMain), from a workspace cwd.
func runPackCapture(t *testing.T, workspace string, args []string) (int, string, string) {
	t.Helper()
	t.Chdir(workspace)
	var out, errw bytes.Buffer
	rc := packMain(args, &out, &errw, false)
	return rc, out.String(), errw.String()
}

const packAddUserTOML = "# my packs\npacks = [\n  # claude is the agent\n  \"claude\",\n]\n"

func TestPackAddSplicesOneItemAndReportsSource(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, packAddUserTOML)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	local := filepath.Join(filepath.Dir(user.Path), "packs", "matt")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"add", "local/matt"})
	if rc != 0 || errw != "" {
		t.Fatalf("pack add rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	for _, want := range []string{user.Path, `Added "local/matt"`, "found at " + local, "read at the next launch"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q: %q", want, out)
		}
	}
	got, _ := os.ReadFile(user.Path)
	for _, keep := range []string{"# my packs\n", "  # claude is the agent\n", `"claude"`, `"local/matt"`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("config lost or lacks %q:\n%s", keep, got)
		}
	}
	rc, out, errw = runPackCapture(t, project.Root, []string{"add", "codex", "--dry-run"})
	_ = rc
	if after, _ := os.ReadFile(user.Path); !bytes.Equal(after, got) {
		t.Fatalf("--dry-run wrote: %s (stdout=%q stderr=%q)", after, out, errw)
	}
}

func TestPackAddRefusesDuplicateNoWrite(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, packAddUserTOML)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, out, errw := runPackCapture(t, project.Root, []string{"add", "claude"})
	if rc == 0 || out != "" || !strings.Contains(errw, "already in") || !strings.Contains(errw, "yolo pack remove claude") {
		t.Fatalf("duplicate add rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if after, _ := os.ReadFile(user.Path); string(after) != packAddUserTOML {
		t.Fatalf("duplicate add wrote: %q", after)
	}
}

func TestPackAddMissingLocalDirectoryNamesTheNextStep(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, packAddUserTOML)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, _, errw := runPackCapture(t, project.Root, []string{"add", "local/nope"})
	if rc == 0 || !strings.Contains(errw, "not a directory") || !strings.Contains(errw, "yolo pack init") {
		t.Fatalf("rc=%d stderr=%q", rc, errw)
	}
	if after, _ := os.ReadFile(user.Path); string(after) != packAddUserTOML {
		t.Fatalf("refused add wrote: %q", after)
	}
}

func TestPackRemoveLeavesNeighborCommentBytes(t *testing.T) {
	project := testconfig.Workspace(t, "")
	const before = "# top\npacks = [\n  # the agent\n  \"claude\",\n  # journal: read the host journal\n  \"journal\", \"host-processes\",\n]\n"
	user := testconfig.User(t, before)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, out, errw := runPackCapture(t, project.Root, []string{"remove", "journal"})
	if rc != 0 || errw != "" || !strings.Contains(out, `Removed "journal"`) {
		t.Fatalf("pack remove rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	got, _ := os.ReadFile(user.Path)
	if strings.Contains(string(got), `"journal"`) || !strings.Contains(string(got), `"host-processes"`) {
		t.Fatalf("remove did not drop exactly the item:\n%s", got)
	}
	for _, keep := range []string{"# top\n", "  # the agent\n", "  # journal: read the host journal\n", `"claude"`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("remove disturbed %q:\n%s", keep, got)
		}
	}
	rc, _, errw = runPackCapture(t, project.Root, []string{"remove", "journal"})
	if rc == 0 || !strings.Contains(errw, "is not in") {
		t.Fatalf("absent remove rc=%d stderr=%q", rc, errw)
	}
}

// A DEFAULT-SELECTED pack cannot be deselected yet (PD-13; its per-workspace `off` is LG-D9's):
// `yolo pack remove` drops a line naming it, but says the pack stays selected, and with no line
// it says why there is nothing to remove instead of implying the pack is already gone.
func TestPackRemoveOfTheDefaultPackSaysItStaysSelected(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = [\"claude\", \"cgroup-delegate\"]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, out, errw := runPackCapture(t, project.Root, []string{"remove", "cgroup-delegate"})
	if rc != 0 || errw != "" || !strings.Contains(out, `Removed "cgroup-delegate" from packs`) ||
		!strings.Contains(out, "is selected by default") || strings.Contains(out, "no longer reads it") {
		t.Fatalf("pack remove rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	rc, out, errw = runPackCapture(t, project.Root, []string{"remove", "cgroup-delegate"})
	if rc == 0 || out != "" || !strings.Contains(errw, "is not in") || !strings.Contains(errw, "is selected by default") ||
		!strings.Contains(errw, "no switch to turn it off") {
		t.Fatalf("absent remove rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
}

// `yolo loopholes status` is where a default-selected pack's loophole is reported (LG-D13): with
// an empty user config it lists cgroup-delegate, which no config line named.
func TestLoopholesStatusShowsTheDefaultSelectedDelegate(t *testing.T) {
	tomlHome(t, "", "")
	rc, stdout, stderr := captureDispatchRC(t, []string{"loopholes", "status"})
	if rc != 0 || !strings.Contains(stdout, "cgroup-delegate") {
		t.Fatalf("loopholes status rc=%d with an empty config does not list cgroup-delegate:\nstdout:\n%s\nstderr:\n%s",
			rc, stdout, stderr)
	}
}

// chosenHostPacks is packs less the default-selected one (cgroup-delegate, PD-13), for a test
// about the packs its config named.
func chosenHostPacks(packs []*packload.Pack) []*packload.Pack {
	var out []*packload.Pack
	for _, p := range packs {
		if !p.SelectedByDefault() {
			out = append(out, p)
		}
	}
	return out
}

func TestPackAddInJailPrintsHostRun(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, packAddUserTOML)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "test-jail")
	for _, verb := range []string{"add", "remove"} {
		rc, out, errw := runPackCapture(t, project.Root, []string{verb, "claude"})
		if rc == 0 || out != "" || !strings.Contains(errw, "yolo host-run -C ") ||
			!strings.Contains(errw, "-- yolo pack "+verb+" claude") {
			t.Fatalf("%s in jail rc=%d stdout=%q stderr=%q", verb, rc, out, errw)
		}
	}
	if after, _ := os.ReadFile(user.Path); string(after) != packAddUserTOML {
		t.Fatalf("jailed pack edit wrote: %q", after)
	}
}

// The pack help's own advice names the verb that selects a pack, not a file to hand-edit, and
// the verb it names is one the same help dispatches.
func TestPackHelpNamesYoloPackAddAsTheWayToSelectAPack(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"--help"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack --help rc=%d: %s", rc, errw.String())
	}
	for _, want := range []string{"yolo pack add claude", `"yolo pack add <name>"`, "pack.toml"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("pack --help lacks %q", want)
		}
	}
	if strings.Contains(out.String(), "configured in ~/.config/yolo-jail/config.jsonc") {
		t.Error("pack --help still tells a reader to hand-edit config.jsonc to select a pack")
	}
}
