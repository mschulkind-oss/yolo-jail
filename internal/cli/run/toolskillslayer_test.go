package run

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

var toolLayerDigest = strings.Repeat("0f", 32)

// writeToolLayerSnapshot writes the jail's snapshot layout under the workspace.
func writeToolLayerSnapshot(t *testing.T, ws, token, tool, name, body string) {
	t.Helper()
	dir := filepath.Join(ws, ".yolo", "tool-skills")
	dd := filepath.Join(dir, toolLayerDigest)
	if err := os.MkdirAll(filepath.Join(dd, "skills", name), 0o755); err != nil {
		t.Fatal(err)
	}
	idx, _ := json.Marshal(map[string]any{"version": 1, "token": token, "digest": toolLayerDigest,
		"skills": []map[string]string{{"tool": tool, "version": "1", "name": name}}})
	for p, b := range map[string]string{
		filepath.Join(dd, "skills", name, "SKILL.md"): body,
		filepath.Join(dd, "index.json"):               string(idx),
		filepath.Join(dir, "current"):                 toolLayerDigest + "\n",
	} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// stagedSkillBodies is every staged `<name>/SKILL.md` for the one skill name, by path.
func stagedSkillBodies(t *testing.T, root, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) == "SKILL.md" && filepath.Base(filepath.Dir(p)) == name {
			b, _ := os.ReadFile(p)
			out[p] = string(b)
		}
		return nil
	})
	return out
}

// The host's tool-skills layer through the REAL call site, on the launch AND the attach path:
// refreshJailBriefings is what both of them run (run.go), so this fails if its call to the
// composition stops passing the layer. It also pins the attach rule: a snapshot the jail wrote
// AFTER the first composition reaches the staged tree on the next call, and a snapshot carrying
// the wrong token never does.
func TestRefreshJailBriefingsComposesTheToolSkillsSnapshotOnEveryCall(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := ctxLaunchHome(t, "")
	t.Setenv("HOME", home)
	writeUserScope(t, `{"install": {"mise": {"packslip:x": "1"}}}`)
	workspace := realWorkspace(t)
	cname := "yolo-tool-skills-layer"
	o := goldenOptions(workspace, home)
	staged := stagedPacks{packs: claudePackFixture(t)}
	cfg := jsonx.NewOrderedMap()
	staging := filepath.Join(paths.AgentsDir(), cname)
	refresh := func() {
		t.Helper()
		if _, err := o.refreshJailBriefings(cname, cfg, "podman", staged, ioprio.Normal, nil); err != nil {
			t.Fatalf("refreshJailBriefings: %v", err)
		}
	}

	refresh() // the launch: the jail has written no snapshot yet
	if got := stagedSkillBodies(t, staging, "tool-fresh"); len(got) != 0 {
		t.Fatalf("a layer without a snapshot: %v", got)
	}

	// A snapshot with a token the host did not mint is never composed.
	writeToolLayerSnapshot(t, workspace, strings.Repeat("0", 64), "packslip:x", "tool-fresh", "FORGED")
	refresh()
	if got := stagedSkillBodies(t, staging, "tool-fresh"); len(got) != 0 {
		t.Fatalf("a snapshot with a forged token was composed: %v", got)
	}

	// The jail's snapshot, written after launch: the next call (an attach) delivers it.
	token, err := config.ToolSkillsToken(workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeToolLayerSnapshot(t, workspace, token, "packslip:x", "tool-fresh", "FROM-THE-JAIL")
	refresh()
	got := stagedSkillBodies(t, staging, "tool-fresh")
	if len(got) == 0 {
		t.Fatalf("the snapshot written after launch did not reach the staged tree under %s", staging)
	}
	for p, b := range got {
		if b != "FROM-THE-JAIL" {
			t.Errorf("%s = %q", p, b)
		}
	}
}

// With no allowlist (the default) the layer is inert: a repository-planted snapshot is not read.
func TestRefreshJailBriefingsReadsNoSnapshotForAnEmptyAllowlist(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := ctxLaunchHome(t, "")
	t.Setenv("HOME", home)
	workspace := realWorkspace(t)
	token, err := config.ToolSkillsToken(workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeToolLayerSnapshot(t, workspace, token, "packslip:x", "tool-fresh", "PLANTED")
	o := goldenOptions(workspace, home)
	cname := "yolo-tool-skills-inert"
	if _, err := o.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
		stagedPacks{packs: claudePackFixture(t)}, ioprio.Normal, nil); err != nil {
		t.Fatal(err)
	}
	if got := stagedSkillBodies(t, filepath.Join(paths.AgentsDir(), cname), "tool-fresh"); len(got) != 0 {
		t.Errorf("a snapshot was composed for an empty allowlist: %v", got)
	}
}

func realWorkspace(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
