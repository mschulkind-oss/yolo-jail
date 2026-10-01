package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// pi-subagents' worktrees default to os.tmpdir(), which a jail deletes on exit — the one
// shipped workflow tool whose default is per-launch (docs/design/durable-scratch-space.md
// §2.4). The pi pack delivers an extension into pi's discovery directory that points it at
// $YOLO_DURABLE_DIR/worktrees/pi-subagents (DS-D4). The declaration is asserted from the
// shipped pack, so a source file with no files contribution fails here.
func TestShippedPiPackDeliversTheDurableWorktreesExtension(t *testing.T) {
	p := shippedPiPack(t)
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-durable-worktrees.js" &&
			c.Into == ".pi/agent/extensions/yolo-durable-worktrees.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("the pi pack does not deliver yolo-durable-worktrees.js to pi's extension directory")
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped pi extensions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-durable-worktrees.js")); err != nil {
		t.Fatalf("pi cannot discover the rendered extension: %v", err)
	}
}

// Executed: the extension sets PI_SUBAGENTS_WORKTREE_DIR from YOLO_DURABLE_DIR when the user
// has not set it, and changes nothing when either is otherwise (a user's own value wins; the
// host notch and a launch with no durable dir export no YOLO_DURABLE_DIR).
func TestPiDurableWorktreesExtensionSetsTheBaseDirOnlyWhenUnset(t *testing.T) {
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-durable-worktrees.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(`
import extension from "./extension.mjs";
await extension({});
process.stdout.write(process.env.PI_SUBAGENTS_WORKTREE_DIR ?? "<unset>");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"durable dir, no user value", []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable"},
			"/workspace/.yolo/durable/worktrees/pi-subagents"},
		{"a user's own value wins", []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable", "PI_SUBAGENTS_WORKTREE_DIR=/mine"},
			"/mine"},
		{"no durable dir", nil, "<unset>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(requireNode(t, "the pi durable-worktrees extension"), harness)
			cmd.Dir = dir
			var env []string
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "YOLO_DURABLE_DIR=") && !strings.HasPrefix(kv, "PI_SUBAGENTS_WORKTREE_DIR=") {
					env = append(env, kv)
				}
			}
			cmd.Env = append(append(env, "HOME="+t.TempDir()), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("executing the extension: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Errorf("PI_SUBAGENTS_WORKTREE_DIR = %q, want %q", out, tc.want)
			}
		})
	}
}
