package entrypoint

// forkpacks_test.go pins the fork rewrite's IN-JAIL call site (loadPackRoot, under LoadJailPacks;
// docs/design/forked-programs-as-packs.md FP-D5): the staged tree holds the base pack's pack.json
// as its author wrote it, so without the rewrite every reader here sees the base's upstream
// delivery for a program a fork builds.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// forkPackTree stages a base pack declaring `pi` via npm and a fork pack building it from source,
// in the no-record layout a hand-built tree has, and returns the tree's root.
func forkPackTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(dir, manifest string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pi", `{"name":"pi","contributes":[{"kind":"program","bin":"pi","via":"npm","package":"pi-coding-agent"}]}`)
	write("pi-matt", `{"name":"pi-matt","contributes":[{"kind":"program","bin":"pi","via":"source",
		"fork_of":"pi","source":"git+https://example.invalid/pi-fork?ref=main","build":"make install",
		"produces":[".local/bin/pi"]}]}`)
	return root
}

// TestTheJailsPackLoaderAppliesForks: LoadJailPacks hands every reader the base's program with the
// fork's delivery. Red if loadPackRoot stops calling packload.ApplyForks.
func TestTheJailsPackLoaderAppliesForks(t *testing.T) {
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": forkPackTree(t)})
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, p := range packs {
		for _, in := range p.Decl.InstallContributions() {
			kinds = append(kinds, p.Name+":"+in.Bin+":"+in.Kind+":"+in.ForkedBy)
		}
	}
	if strings.Join(kinds, ",") != "pi:pi:"+packdecl.InstallKindSource+":pi-matt" {
		t.Errorf("the jail's programs = %v, want pi's one program carrying pi-matt's build", kinds)
	}
}

// TestNoLauncherDeliversTheBasesUpstreamProgramUnderAForksName: the launcher generator reads the
// rewritten program and — until the source launcher is built — writes no launcher for it, saying
// so by name. Before the rewrite it wrote the base's npm launcher: the upstream program, under the
// fork's name.
func TestNoLauncherDeliversTheBasesUpstreamProgramUnderAForksName(t *testing.T) {
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": forkPackTree(t)})
	e.Stderr = &stderr
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(e.LaunchDir(), "pi")
	if body, err := os.ReadFile(launcher); err == nil && strings.Contains(string(body), "npm install") {
		t.Fatalf("the base's npm launcher was written for a forked program:\n%s", body)
	}
	if !strings.Contains(stderr.String(), `no launcher for "pi" — fork pack pi-matt builds it`) {
		t.Errorf("the undelivered fork is not named:\n%s", stderr.String())
	}
}
