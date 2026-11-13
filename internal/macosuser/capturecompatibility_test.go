package macosuser

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestCapturePlanCompatibilityOwnsItsResolvedContext(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        Mode
		version     string
		refuse      bool
		invalidMode bool
	}{
		{"account", ModeAccount, "1.0.103", false, false},
		{"self-compatible", ModeSelf, "1.0.92", false, false},
		{"self-affected", ModeSelf, "1.0.103", true, false},
		{"invalid-mode", Mode(2), "1.0.103", false, true},
	} {
		for _, incoming := range []struct {
			name  string
			spoof bool
		}{
			{"absent", false},
			{"spoofed", true},
		} {
			t.Run(tc.name+"/"+incoming.name, func(t *testing.T) {
				t.Cleanup(entrypoint.OverrideImageProbeBase(t.TempDir()))
				t.Cleanup(packload.OverrideSkewTolerance(false))
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				pack := filepath.Join(root, "copilot")
				if err := os.MkdirAll(pack, 0755); err != nil {
					t.Fatal(err)
				}
				// The manifest, and the compatibility record its home/ tree delivers by
				// convention to ~/.config/yolo-program-compatibility/copilot.json.
				for _, name := range []string{shippedPackManifestName(t, "copilot"),
					filepath.Join("home", ".config", "yolo-program-compatibility", "copilot.json")} {
					data, err := os.ReadFile(filepath.Join("..", "..", "packs", "copilot", name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Dir(filepath.Join(pack, name)), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(pack, name), data, 0644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(root, "_pack-tree.json"),
					[]byte(`{"packs":[{"name":"copilot","dir":"copilot"}]}`), 0644); err != nil {
					t.Fatal(err)
				}
				tempDir := func() string {
					t.Helper()
					p, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				opts := testCaptureOptions()
				opts.Bin, opts.Mode, opts.CaptureRoot, opts.HostPackRoot = "copilot", tc.mode, tempDir(), root
				opts.HostHome = tempDir()
				opts.DarwinUserTempDir = filepath.Join(opts.HostHome, "temp")
				if incoming.spoof {
					opts.SandboxEnv.Set(paths.MacosUserTmpdirContextEnv,
						`{"mode":2,"workspace":"/spoof","home":"/spoof"}`)
				}
				original, originalPresent := opts.SandboxEnv.Get(paths.MacosUserTmpdirContextEnv)
				plan := BuildCapturePlan(opts)
				if got, present := opts.SandboxEnv.Get(paths.MacosUserTmpdirContextEnv); present != originalPresent || got != original {
					t.Fatalf("BuildCapturePlan mutated its caller's compatibility context: got %v, present=%v; want %v, present=%v",
						got, present, original, originalPresent)
				}
				vars := map[string]string{"JAIL_HOME": plan.StagingHome, "YOLO_PACK_ROOT": root, entrypoint.AgentUpdatesEnv: "false"}
				for _, key := range []string{"YOLO_DARWIN_WORKSPACE", paths.MacosUserTmpdirContextEnv} {
					if value, ok := argvEnvValue(plan.BootstrapArgv, key); ok {
						vars[key] = value
					}
				}
				// The staged fixture stands for StagePackCommands' sealed guest copy. The executable
				// below is test-owned and PATH-local; it accepts only --version and cannot reach a real CLI.
				e := entrypoint.DarwinEnvFrom(vars, plan.StagingHome)
				e.Stderr = io.Discard
				if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(e.LocalBin(), 0755); err != nil {
					t.Fatal(err)
				}
				fake := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 91\nprintf '%s\\n' '" + tc.version + "'\n"
				if err := os.WriteFile(filepath.Join(e.LocalBin(), "copilot"), []byte(fake), 0755); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(filepath.Join(e.LaunchDir(), "copilot"))
				cmd.Env = append(os.Environ(), "HOME="+plan.StagingHome,
					"PATH="+e.LocalBin()+string(os.PathListSeparator)+os.Getenv("PATH"), "YOLO_INSTALL_ONLY=1")
				out, err := cmd.CombinedOutput()
				invalid := strings.Contains(string(out), "invalid macos-user launch context")
				refused := strings.Contains(string(out), "macos-user self's required SecurityServer deny")
				wantFailure := tc.refuse || tc.invalidMode
				if invalid != tc.invalidMode || refused != tc.refuse || (err != nil) != wantFailure {
					t.Fatalf("capture context/guard mismatch: invalid=%v wantInvalid=%v refused=%v wantRefused=%v err=%v output=%s",
						invalid, tc.invalidMode, refused, tc.refuse, err, out)
				}
				raw, ok := argvEnvValue(plan.BootstrapArgv, paths.MacosUserTmpdirContextEnv)
				session, present := sandboxEnvFileValue(plan.EnvFileContent, paths.MacosUserTmpdirContextEnv)
				if tc.invalidMode {
					if ok || present {
						t.Fatalf("invalid capture mode received a valid compatibility context: bootstrap=%q session=%q", raw, session)
					}
					return
				}
				ctx, ctxErr := ParseSandboxTmpdirValidationContext(raw)
				if !ok || ctxErr != nil || !present || session != raw || ctx.Mode != tc.mode ||
					ctx.Workspace != plan.StagingRoot || ctx.Home != plan.StagingHome {
					t.Fatalf("capture-owned bootstrap/driver context missing or unequal: %q %q %v", raw, session, ctxErr)
				}
				// The account capture's (allow default) profile grants /tmp and all of
				// /var/folders (SeatbeltCaptureProfile); its context names exactly those, never
				// the owner's temp dir (MMP-D36).
				if tc.mode == ModeAccount && (ctx.DarwinUserTempDir != accountCaptureTempRoot || !ctx.SharedTmp) {
					t.Fatalf("account capture context does not name its profile's temp grants: %+v", ctx)
				}
				if tc.mode == ModeSelf && (ctx.DarwinUserTempDir != opts.DarwinUserTempDir || ctx.SharedTmp != opts.SharedTmp) {
					t.Fatalf("self capture context omitted profile-granted temp roots: %+v", ctx)
				}
			})
		}
	}
}
