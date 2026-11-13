package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

type sandboxTmpdirLaunch struct {
	home, root, yoloBin string
	launchers           map[string]string
	reports             map[string]string
}

func stageSandboxTmpdirPacks(t *testing.T, shippedClaude bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	official := filepath.Join(root, "_official")
	if err := os.MkdirAll(official, 0o755); err != nil {
		t.Fatal(err)
	}
	if shippedClaude {
		packs, problems := packload.MaterializeEmbedded(officialpacks.FS, official)
		if len(problems) > 0 {
			t.Fatalf("materializing official packs: %v", problems)
		}
		if len(packs) == 0 {
			t.Fatal("no official packs materialized")
		}
	}
	custom := filepath.Join(official, "tmp-followers")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tmp-followers","contributes":[
		{"kind":"program","bin":"generic","via":"installer","url":"https://example.invalid/install.sh","sandbox_tmpdir_env":["GENERIC_TMPDIR"]},
		{"kind":"program","bin":"native-generic","via":"installer","url":"https://example.invalid/install.sh","sandbox_tmpdir_env":["NATIVE_GENERIC_TMPDIR"]},
		{"kind":"program","bin":"unrelated","via":"installer","url":"https://example.invalid/install.sh"}
	]}`
	if err := os.WriteFile(filepath.Join(custom, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func newSandboxTmpdirLaunch(t *testing.T, shippedClaude bool) *sandboxTmpdirLaunch {
	return newSandboxTmpdirLaunchWithContext(t, shippedClaude, true, true)
}

func newSandboxTmpdirLaunchWithContext(t *testing.T, shippedClaude, includeContext, nativeMarker bool) *sandboxTmpdirLaunch {
	return newSandboxTmpdirLaunchWithMode(t, shippedClaude, includeContext, nativeMarker, macosuser.ModeSelf)
}

func newSandboxTmpdirLaunchWithMode(t *testing.T, shippedClaude, includeContext, nativeMarker bool, mode macosuser.Mode) *sandboxTmpdirLaunch {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	image := t.TempDir()
	t.Cleanup(entrypoint.OverrideImageProbeBase(image))
	home := t.TempDir()
	root := stageSandboxTmpdirPacks(t, shippedClaude)
	workspace := filepath.Join(home, "workspace")
	userTmp := filepath.Join(home, "user temp")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	// The temp dir of the user the sandbox runs as, in either mode: the account mode's plan bakes
	// the account's own into this context (MMP-D36), as self mode bakes yours.
	contextData := macosuser.SandboxTmpdirValidationContext{
		Mode: mode, Workspace: workspace, Home: home, DarwinUserTempDir: userTmp,
	}
	context, err := macosuser.MarshalSandboxTmpdirValidationContext(contextData)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"JAIL_HOME":                 home,
		"YOLO_PACK_ROOT":            root,
		"YOLO_WORKSPACE":            workspace,
		"YOLO_NO_PROGRAM_READINESS": "1",
	}
	if nativeMarker {
		vars["YOLO_DARWIN_WORKSPACE"] = workspace
	}
	if includeContext {
		vars[paths.MacosUserTmpdirContextEnv] = string(context)
	}
	e := entrypoint.NewEnv(vars)
	e.Stderr = io.Discard
	// GenerateAgentLaunchers reads packs through the in-jail loader, which switches this
	// process-wide decoder to tolerant. This host-side fixture keeps later authoring reads strict.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers: %v", err)
	}

	launchers := make(map[string]string)
	reports := make(map[string]string)
	for _, bin := range []string{"claude", "generic", "native-generic", "unrelated"} {
		launcher := filepath.Join(e.LaunchDir(), bin)
		if _, err := os.Stat(launcher); err != nil {
			if bin == "claude" && !shippedClaude {
				continue
			}
			t.Fatalf("generated %s launcher: %v", bin, err)
		}
		launchers[bin] = launcher
		report := filepath.Join(home, bin+"-env")
		reports[bin] = report
		real := filepath.Join(home, ".local", "bin", bin)
		if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/bash\n" +
			"printf 'TMPDIR=%s\\nCLAUDE_CODE_TMPDIR=%s\\nGENERIC_TMPDIR=%s\\nNATIVE_GENERIC_TMPDIR=%s\\n' " +
			"\"${TMPDIR-}\" \"${CLAUDE_CODE_TMPDIR-}\" \"${GENERIC_TMPDIR-}\" \"${NATIVE_GENERIC_TMPDIR-}\" > " +
			shquote.Quote(report) + "\n"
		if err := os.WriteFile(real, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	yoloBin := filepath.Join(home, "fake-yolo-bin")
	if err := os.MkdirAll(yoloBin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	yolo := "#!/bin/sh\nexec " + shquote.Quote(exe) + " " + testAsYoloArg + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(yoloBin, "yolo"), []byte(yolo), 0o755); err != nil {
		t.Fatal(err)
	}
	return &sandboxTmpdirLaunch{home: home, root: root, yoloBin: yoloBin,
		launchers: launchers, reports: reports}
}

func (h *sandboxTmpdirLaunch) run(t *testing.T, name string, env map[string]string) (map[string]string, []byte, error) {
	t.Helper()
	launcher, ok := h.launchers[name]
	if !ok {
		t.Fatalf("no generated %s launcher", name)
	}
	report := h.reports[name]
	_ = os.Remove(report)
	cmd := exec.Command(launcher, "--version")
	cmd.Dir = h.home
	path := h.yoloBin + string(os.PathListSeparator) + os.Getenv("PATH")
	cmd.Env = []string{"HOME=" + h.home, "PATH=" + path}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	data, readErr := os.ReadFile(report)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values, out, err
}

// MMP-D36: an account-mode nested launch keeps the account's TMPDIR, which its profile grants, and
// its program follower follows it; a TMPDIR outside the account mode's roots is refused, since the
// profile now closes the shared /tmp there too (MMP-D29).
func TestAccountNestedTempFollowerPreservesAccountTMPDIR(t *testing.T) {
	h := newSandboxTmpdirLaunchWithMode(t, true, true, true, macosuser.ModeAccount)
	accountTmp := filepath.Join(h.home, "user temp")
	got, out, err := h.run(t, "claude", map[string]string{"TMPDIR": accountTmp})
	if err != nil {
		t.Fatalf("account nested Claude launch failed: %v\n%s", err, out)
	}
	if got["TMPDIR"] != accountTmp || got["CLAUDE_CODE_TMPDIR"] != accountTmp {
		t.Errorf("account nested Claude env = %#v, want actual execution with both temp variables %q", got, accountTmp)
	}
	if _, out, err := h.run(t, "claude", map[string]string{"TMPDIR": "/tmp/elsewhere"}); err == nil ||
		!strings.Contains(string(out), "TMPDIR=/tmp/elsewhere") {
		t.Errorf("account nested Claude launch accepted a TMPDIR its profile does not grant: %v\n%s", err, out)
	}
}

func TestShippedClaudeLauncherCarriesNativeTMPDIRToNestedClaude(t *testing.T) {
	h := newSandboxTmpdirLaunch(t, true)
	tmp := filepath.Join(h.home, "workspace", "tmp with 'quote")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	native := map[string]string{"TMPDIR": tmp}
	got, out, err := h.run(t, "claude", native)
	if err != nil {
		t.Fatalf("native nested Claude launch failed: %v\n%s", err, out)
	}
	if got["TMPDIR"] != tmp || got["CLAUDE_CODE_TMPDIR"] != tmp {
		t.Errorf("nested Claude env = %#v, want CLAUDE_CODE_TMPDIR to follow %q", got, tmp)
	}

	// A safe explicit override remains authoritative and is allowed by the same native roots.
	explicit := filepath.Join(h.home, "workspace", "explicit 'Claude temp")
	got, out, err = h.run(t, "claude", map[string]string{
		"TMPDIR": tmp, "CLAUDE_CODE_TMPDIR": explicit,
		"YOLO_DARWIN_WORKSPACE": filepath.Join(h.home, "workspace"),
	})
	if err != nil {
		t.Fatalf("native nested Claude launch with safe override failed: %v\n%s", err, out)
	}
	if got["CLAUDE_CODE_TMPDIR"] != explicit {
		t.Errorf("safe explicit CLAUDE_CODE_TMPDIR = %q, want %q", got["CLAUDE_CODE_TMPDIR"], explicit)
	}

	// Shell-looking bytes remain an ordinary path value and never execute.
	marker := filepath.Join(h.home, "workspace", "injected")
	payload := tmp + "'; touch " + marker + "; #"
	got, out, err = h.run(t, "claude", map[string]string{
		"TMPDIR": payload, "YOLO_DARWIN_WORKSPACE": filepath.Join(h.home, "workspace"),
	})
	if err != nil {
		t.Fatalf("native nested Claude launch rejected a workspace-local opaque path: %v\n%s", err, out)
	}
	if got["CLAUDE_CODE_TMPDIR"] != payload {
		t.Errorf("Claude temp follower = %q, want the opaque TMPDIR value", got["CLAUDE_CODE_TMPDIR"])
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("TMPDIR environment data executed shell syntax (marker stat error %v)", err)
	}
}

func TestUnsafeNestedClaudeTempOverrideRefusesBeforeProgramExec(t *testing.T) {
	h := newSandboxTmpdirLaunch(t, true)
	tmp := filepath.Join(h.home, "workspace", "safe tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside temp")
	got, out, err := h.run(t, "claude", map[string]string{
		"TMPDIR": tmp, "CLAUDE_CODE_TMPDIR": outside,
	})
	if err == nil {
		t.Fatalf("unsafe nested Claude temp override was accepted; program env %#v", got)
	}
	if !strings.Contains(string(out), "CLAUDE_CODE_TMPDIR") {
		t.Errorf("unsafe override refusal omitted its variable name: %s", out)
	}
	if _, statErr := os.Stat(h.reports["claude"]); !os.IsNotExist(statErr) {
		t.Errorf("Claude ran despite an unsafe temp override (stat error %v)", statErr)
	}
}

func TestNestedTempFollowerIsNativeSelectedAndDeclaredOnly(t *testing.T) {
	h := newSandboxTmpdirLaunch(t, true)
	tmp := filepath.Join(h.home, "workspace", "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}

	// An arbitrary declared program uses the same contract; this is not Claude-specific.
	got, out, err := h.run(t, "generic", map[string]string{
		"TMPDIR": tmp, "YOLO_DARWIN_WORKSPACE": filepath.Join(h.home, "workspace"),
	})
	if err != nil || got["GENERIC_TMPDIR"] != tmp {
		t.Errorf("native generic declared follower = %#v, %v\n%s", got, err, out)
	}

	got, out, err = h.run(t, "native-generic", map[string]string{"TMPDIR": tmp})
	if err != nil || got["NATIVE_GENERIC_TMPDIR"] != tmp {
		t.Errorf("native generated launcher declared follower = %#v, %v\n%s", got, err, out)
	}

	// An undeclared program and a plain shell do not gain a follower.
	got, out, err = h.run(t, "unrelated", map[string]string{
		"TMPDIR": tmp, "YOLO_DARWIN_WORKSPACE": filepath.Join(h.home, "workspace"),
	})
	if err != nil || got["CLAUDE_CODE_TMPDIR"] != "" || got["GENERIC_TMPDIR"] != "" ||
		got["NATIVE_GENERIC_TMPDIR"] != "" {
		t.Errorf("unrelated generated program acquired temp followers: %#v, %v\n%s", got, err, out)
	}
	cmd := exec.Command("bash", "-c", `test -z "${CLAUDE_CODE_TMPDIR+x}"`)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("plain bash acquired a Claude temp variable: %v\n%s", err, out)
	}

	// A container-marker control and a host control must not adapt an otherwise declared program.
	nonNative := newSandboxTmpdirLaunchWithContext(t, true, false, false)
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"container", map[string]string{"TMPDIR": tmp, paths.JailBackendEnv: "podman"}},
		{"host", map[string]string{
			"TMPDIR": tmp, "YOLO_DARWIN_WORKSPACE": filepath.Join(nonNative.home, "workspace"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, program := range []struct {
				bin, variable string
			}{{"claude", "CLAUDE_CODE_TMPDIR"}, {"native-generic", "NATIVE_GENERIC_TMPDIR"}} {
				got, out, err := nonNative.run(t, program.bin, tc.env)
				if err != nil || got[program.variable] != "" {
					t.Errorf("non-native %s %s launcher adapted temp env: %#v, %v\n%s",
						tc.name, program.bin, got, err, out)
				}
			}
		})
	}

	// When Claude is absent from the staged selection, no Claude launcher is emitted to carry the
	// declaration into execution.
	unselected := newSandboxTmpdirLaunch(t, false)
	if _, ok := unselected.launchers["claude"]; ok {
		t.Fatal("unselected shipped Claude generated a launcher")
	}
}

func TestNativeNestedTempFollowerRefusesWithoutResolvedMetadata(t *testing.T) {
	h := newSandboxTmpdirLaunchWithContext(t, true, false, true)
	tmp := filepath.Join(h.home, "workspace", "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	_, out, err := h.run(t, "claude", map[string]string{"TMPDIR": tmp})
	if err == nil || !strings.Contains(string(out), "native temp-validation metadata is missing") {
		t.Fatalf("native launch without producer-resolved metadata was not refused: err=%v\n%s", err, out)
	}
	if _, statErr := os.Stat(h.reports["claude"]); !os.IsNotExist(statErr) {
		t.Errorf("Claude ran without validation metadata (stat error %v)", statErr)
	}
}

func TestNestedTempFollowerInstallOnlyDoesNotExecClaude(t *testing.T) {
	h := newSandboxTmpdirLaunch(t, true)
	tmp := filepath.Join(h.home, "workspace", "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	_, out, err := h.run(t, "claude", map[string]string{
		"TMPDIR": tmp, "YOLO_DARWIN_WORKSPACE": filepath.Join(h.home, "workspace"),
		entrypoint.InstallOnlyEnv: "1",
	})
	if err != nil {
		t.Fatalf("native Claude install-only launcher failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(h.reports["claude"]); !os.IsNotExist(statErr) {
		t.Errorf("install-only mode executed Claude (stat error %v)", statErr)
	}
}

func TestNestedTempValidationContextUsesResolvedRoots(t *testing.T) {
	h := newSandboxTmpdirLaunch(t, true)
	body, err := os.ReadFile(h.launchers["claude"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "SANDBOX_TMPDIR_CONTEXT='") {
		t.Fatalf("generated native launcher did not bake validation context: %s", body)
	}
	if !strings.Contains(string(body), filepath.Join(h.home, "workspace")) {
		t.Fatal("generated launcher context omitted the resolved workspace root")
	}
}

func TestNestedTmpdirLauncherFixtureRestoresStrictPackReads(t *testing.T) {
	t.Run("generate launchers", func(t *testing.T) {
		newSandboxTmpdirLaunch(t, false)
	})

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"probe","contributes":[`+
		`{"kind":"env","vars":{"A":"1"},"field_from_a_newer_yolo":1}]}`)
	if _, problems := packload.LoadDir(dir, "probe"); len(problems) == 0 ||
		!strings.Contains(strings.Join(problems, "\n"), `unknown field "field_from_a_newer_yolo"`) {
		t.Fatalf("the strict authoring read after fixture generation must reject the unknown field by name, got %v", problems)
	}
}
