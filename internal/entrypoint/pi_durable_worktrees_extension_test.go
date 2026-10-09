package entrypoint

import (
	"encoding/json"
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

// Executed: the extension configures worktreeBaseDir in subagent/config.json from
// YOLO_DURABLE_DIR when the user has not set it, and changes nothing when either is
// otherwise (a user's own value wins; the host notch and a launch with no durable dir export
// no YOLO_DURABLE_DIR). PI_SUBAGENTS_WORKTREE_DIR is never exported into the process
// environment, so child processes do not inherit it.
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
import extension, { subagentConfigPath } from "./extension.mjs";
import fs from "node:fs";

await extension({});

const envVal = process.env.PI_SUBAGENTS_WORKTREE_DIR ?? "<unset>";
const configPath = subagentConfigPath(process.env);
let fileVal = "<no-file>";
if (fs.existsSync(configPath)) {
	try {
		const parsed = JSON.parse(fs.readFileSync(configPath, "utf8"));
		fileVal = parsed.worktreeBaseDir ?? "<no-key>";
	} catch (e) {
		fileVal = "<parse-error>";
	}
}
process.stdout.write(JSON.stringify({ env: envVal, file: fileVal }));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		env      []string
		initFile string
		wantEnv  string
		wantFile string
	}{
		{
			name:     "durable dir, no user value",
			env:      []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable"},
			wantEnv:  "<unset>",
			wantFile: "/workspace/.yolo/durable/worktrees/pi-subagents",
		},
		{
			name:     "a user's own env value wins",
			env:      []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable", "PI_SUBAGENTS_WORKTREE_DIR=/mine"},
			wantEnv:  "/mine",
			wantFile: "<no-file>",
		},
		{
			name:     "no durable dir",
			env:      nil,
			wantEnv:  "<unset>",
			wantFile: "<no-file>",
		},
		{
			name:     "a user's own worktreeBaseDir in config.json wins",
			env:      []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable"},
			initFile: `{"worktreeBaseDir": "/user/configured"}`,
			wantEnv:  "<unset>",
			wantFile: "/user/configured",
		},
		{
			name:     "an existing yolo-managed worktreeBaseDir updates when durable dir changes",
			env:      []string{"YOLO_DURABLE_DIR=/workspace/.yolo/durable"},
			initFile: `{"worktreeBaseDir": "/old/path", "_yoloManagedWorktreeBaseDir": true}`,
			wantEnv:  "<unset>",
			wantFile: "/workspace/.yolo/durable/worktrees/pi-subagents",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.initFile != "" {
				cfgPath := filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json")
				if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(cfgPath, []byte(tc.initFile), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(requireNode(t, "the pi durable-worktrees extension"), harness)
			cmd.Dir = dir
			var env []string
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "YOLO_DURABLE_DIR=") && !strings.HasPrefix(kv, "PI_SUBAGENTS_WORKTREE_DIR=") {
					env = append(env, kv)
				}
			}
			cmd.Env = append(append(env, "HOME="+home), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("executing the extension: %v\n%s", err, out)
			}
			var got struct {
				Env  string `json:"env"`
				File string `json:"file"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("parsing output %q: %v", out, err)
			}
			if got.Env != tc.wantEnv {
				t.Errorf("PI_SUBAGENTS_WORKTREE_DIR in env = %q, want %q", got.Env, tc.wantEnv)
			}
			if got.File != tc.wantFile {
				t.Errorf("worktreeBaseDir in config.json = %q, want %q", got.File, tc.wantFile)
			}
		})
	}
}
