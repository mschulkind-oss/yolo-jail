package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func selectedNPMConsumer(t *testing.T, f *selectedNPMFixture, r npmCaptureRecord) (string, *entrypoint.Env) {
	t.Helper()
	restore := packload.OverrideSkewTolerance(false)
	t.Cleanup(restore)
	home := t.TempDir()
	root := t.TempDir()
	pack := filepath.Join(root, "fixture")
	if err := os.MkdirAll(pack, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"contributes":[{"kind":"program","bin":"probe","via":"npm","package":"probe-pkg"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	inputs, err := r.Resolution.inputs()
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(map[string]entrypoint.ProgramCaptureDelivery{"probe": {Declared: f.in.Package, Compatibility: r.Identity, Platform: capture.Platform(), Runtime: inputs.Runtime, NodeFloor: f.in.NodeFloor}})
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws"), "YOLO_PACK_ROOT": root, entrypoint.CapturesDirEnv: f.store.Dir, entrypoint.ProgramCapturesEnv: string(wire)})
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	return home, e
}

func runSelectedConsumer(t *testing.T, f *selectedNPMFixture, home string, e *entrypoint.Env, mode string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(e.LaunchDir(), "probe"))
	cmd.Env = append(os.Environ(), "HOME="+home, "NPM_CONFIG_PREFIX="+e.NpmPrefix, "PATH="+f.tools+":"+os.Getenv("PATH"), "YOLO_INSTALL_ONLY=0", "YOLO_PACK_UPDATE=0")
	if mode != "" {
		cmd.Env = append(cmd.Env, mode+"=1")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("consumer %s: %v\n%s", mode, err, out)
	}
	return string(out)
}

func TestSelectedNPMGeneratedReadinessConsumesExactArtifactsWithoutInstaller(t *testing.T) {
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	home, e := selectedNPMConsumer(t, f, record)
	if out := runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv); strings.Contains(out, "1.0.0") {
		t.Fatal("readiness ran synthetic target")
	}
	markerPath := entrypoint.ProgramCaptureMarker(home, "probe")
	marker, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(marker)) != record.Identity {
		t.Fatalf("prepared materialization marker = %q, %v; want exact launch identity %q", marker, err, record.Identity)
	}
	spec, err := os.ReadFile(filepath.Join(home, ".cache/yolo-agent-stamps/probe.spec"))
	if err != nil || strings.TrimSpace(string(spec)) != "probe-pkg@latest" {
		t.Fatalf("prepared install spec = %q, %v; want exact declared install spec", spec, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache/yolo-agent-stamps/probe.stamp")); !os.IsNotExist(err) {
		t.Fatalf("prepared materialization unexpectedly wrote or required legacy stamp: %v", err)
	}
	if got := runSelectedConsumer(t, f, home, e, ""); strings.TrimSpace(got) != "1.0.0" {
		t.Fatalf("target %q", got)
	}
	if got := runSelectedConsumer(t, f, home, e, entrypoint.FreshRefreshEnv); strings.Contains(got, "1.0.0") {
		t.Fatal("refresh ran target")
	}
	counts, _ := os.ReadFile(filepath.Join(f.root, "counts"))
	if strings.Count(string(counts), "install\n") != 1 || strings.Count(string(counts), "resolve\n") != 1 {
		t.Fatalf("consumer contacted installer or registry: %s", counts)
	}
	// A newer admission has no authority to update a running launch's fixed vector.
	f.version(t, "2.0.0")
	next := f.advance(t, true)
	if got := runSelectedConsumer(t, f, home, e, ""); strings.TrimSpace(got) != "1.0.0" {
		t.Fatalf("running generation moved: %q", got)
	}
	otherHome, other := selectedNPMConsumer(t, f, next)
	if got := runSelectedConsumer(t, f, otherHome, other, ""); !strings.HasSuffix(strings.TrimSpace(got), "2.0.0") {
		t.Fatalf("next launch did not consume new admission: %q", got)
	}
}

func TestSelectedNPMPreparedMarkerMismatchRematerializesAndMissingPayloadFails(t *testing.T) {
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	home, e := selectedNPMConsumer(t, f, record)
	runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv)
	markerPath := entrypoint.ProgramCaptureMarker(home, "probe")
	markerBefore, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(markerBefore)) != record.Identity {
		t.Fatalf("initial prepared marker = %q, %v; want %q", markerBefore, err, record.Identity)
	}
	if err := os.WriteFile(markerPath, []byte("stale-generation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv)
	markerAfter, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(markerAfter)) != record.Identity {
		t.Fatalf("mismatched marker was not repaired to launch identity: %q, %v", markerAfter, err)
	}

	// A valid marker cannot mask a missing admitted payload: every invocation validates
	// the exact capture before accepting the installed binary or executing it.
	captured, err := f.store.Resolve(record.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(captured.Root); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(e.LaunchDir(), "probe"))
	cmd.Env = append(os.Environ(), "HOME="+home, "NPM_CONFIG_PREFIX="+e.NpmPrefix,
		"PATH="+f.tools+":"+os.Getenv("PATH"), "YOLO_INSTALL_ONLY=1", "YOLO_PACK_UPDATE=0")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "run `yolo capture probe` to make it") {
		t.Fatalf("missing prepared payload was accepted or lacked the capture repair step: err=%v\n%s", err, out)
	}
	markerAfter, err = os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(markerAfter)) != record.Identity {
		t.Fatalf("missing-payload refusal changed the launch marker: %q, %v", markerAfter, err)
	}
	counts, _ := os.ReadFile(filepath.Join(f.root, "counts"))
	if strings.Count(string(counts), "install\n") != 1 || strings.Count(string(counts), "resolve\n") != 1 {
		t.Fatalf("prepared recovery/refusal contacted mutable installer: %s", counts)
	}
}

func TestSelectedNPMGeneratedLegacyConsumerClaimsItsJailStartCheck(t *testing.T) {
	f := newSelectedNPMFixture(t)
	home := t.TempDir()
	root := t.TempDir()
	pack := filepath.Join(root, "fixture")
	if err := os.MkdirAll(pack, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"contributes":[{"kind":"program","bin":"probe","via":"npm","package":"probe-pkg"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	// No ProgramCapturesEnv: this is the real generated launcher’s ordinary installer
	// route, so what that route records stays covered by an actual consumer. It records the per-start
	// claim marker (docs/design/launcher-update-per-jail-start.md LU-D1, LU-D9), not an hourly stamp; the
	// boot's jail-start record, which only the boot writes, is the fixture's.
	const startID = "0123456789abcdef0123456789abcdef"
	startRecord := filepath.Join(home, filepath.FromSlash(entrypoint.JailStartRel))
	if err := os.MkdirAll(filepath.Dir(startRecord), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(startRecord, []byte(startID+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws"),
		"YOLO_PACK_ROOT": root, entrypoint.CapturesDirEnv: f.store.Dir})
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if out := runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv); strings.Contains(out, "1.0.0") {
		t.Fatalf("legacy readiness ran target: %s", out)
	}
	// The claimed check says "is already current" before the program's own output.
	got := strings.Split(strings.TrimSpace(runSelectedConsumer(t, f, home, e, "")), "\n")
	if got[len(got)-1] != "1.0.0" {
		t.Fatalf("legacy launcher version = %q", got)
	}
	// The ordinary run after the cold install is the one that claims the jail start's check.
	claim := filepath.Join(home, filepath.FromSlash(entrypoint.LaunchChecksDirRel), "probe."+startID)
	if _, err := os.Stat(claim); err != nil {
		t.Fatalf("legacy installer route did not claim this jail start's check: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache/yolo-agent-stamps/probe.stamp")); !os.IsNotExist(err) {
		t.Fatalf("legacy installer route wrote the hourly stamp LU-D1 removed: %v", err)
	}
	if _, err := os.Stat(entrypoint.ProgramCaptureMarker(home, "probe")); !os.IsNotExist(err) {
		t.Fatalf("legacy consumer unexpectedly wrote a prepared marker: %v", err)
	}
}

func TestSelectedNPMEnabledMCPRefreshConsumesExactDecision(t *testing.T) {
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	_, e := selectedNPMConsumer(t, f, record)
	if err := entrypoint.RefreshServers(entrypoint.ServerRefreshRequest{Env: e, NpmSpecs: f.in.Package, Updates: true, Stderr: io.Discard, Run: func(_ctx context.Context, _w io.Writer, argv []string) error {
		if argv[0] != "yolo" || argv[2] != "capture-materialize" {
			t.Fatalf("MCP consumer ran mutable installer: %v", argv)
		}
		if rc := runInternal(argv[2:]); rc != 0 {
			return fmt.Errorf("materializer status %d", rc)
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedNPMWarmGeneratedConsumersRejectChangedRuntime(t *testing.T) {
	for _, mode := range []string{entrypoint.InstallOnlyEnv, entrypoint.FreshRefreshEnv, "execute", "reentry"} {
		t.Run(mode, func(t *testing.T) {
			f := newSelectedNPMFixture(t)
			record := f.advance(t, false)
			home, e := selectedNPMConsumer(t, f, record)
			runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv)
			markerBefore, _ := os.ReadFile(entrypoint.ProgramCaptureMarker(home, "probe"))
			writeFile(t, npmCaptureImagePath, "changed-consumer-runtime")
			cmd := exec.Command(filepath.Join(e.LaunchDir(), "probe"))
			cmd.Env = append(os.Environ(), "HOME="+home, "NPM_CONFIG_PREFIX="+e.NpmPrefix, "PATH="+f.tools+":"+os.Getenv("PATH"), "YOLO_INSTALL_ONLY=0", "YOLO_PACK_UPDATE=0")
			if mode == "reentry" {
				cmd.Env = append(cmd.Env, "_YOLO_LAUNCHER_ACTIVE=:probe")
			} else if mode != "execute" {
				cmd.Env = append(cmd.Env, mode+"=1")
			}
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("matching marker hid changed runtime; consumer succeeded: %s", out)
			}
			if !strings.Contains(string(out), "fresh launch") {
				t.Fatalf("refusal lacks next step: %s", out)
			}
			markerAfter, _ := os.ReadFile(entrypoint.ProgramCaptureMarker(home, "probe"))
			if string(markerBefore) != string(markerAfter) {
				t.Fatal("validation mutated running generation")
			}
			counts, _ := os.ReadFile(filepath.Join(f.root, "counts"))
			if strings.Count(string(counts), "install\n") != 1 || strings.Count(string(counts), "resolve\n") != 1 {
				t.Fatalf("validation contacted registry/installer: %s", counts)
			}
		})
	}
}

func TestSelectedNPMWarmMCPRejectsChangedRuntime(t *testing.T) {
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	_, e := selectedNPMConsumer(t, f, record)
	refresh := func() error {
		return entrypoint.RefreshServers(entrypoint.ServerRefreshRequest{Env: e, NpmSpecs: f.in.Package, Updates: true, Stderr: io.Discard, Run: func(ctx context.Context, w io.Writer, argv []string) error {
			if argv[0] != "yolo" || argv[2] != "capture-materialize" {
				t.Fatalf("mutable MCP installer: %v", argv)
			}
			if rc := runInternal(argv[2:]); rc != 0 {
				return fmt.Errorf("materializer status %d", rc)
			}
			return nil
		}})
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, npmCaptureImagePath, "changed-MCP-runtime")
	if err := refresh(); err == nil {
		t.Fatal("warm MCP skipped changed runtime validation")
	}
}

// A PATH shim is a selector, not the executable whose bytes identify Node. The
// actual generated launcher must bind validation AND target execution to the
// selected physical Node, with its npm observed on the sealed runtime PATH.
func TestSelectedNPMGeneratedConsumerBindsShimSelectionToGuardAndExecution(t *testing.T) {
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	home, e := selectedNPMConsumer(t, f, record)
	shims := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" != -p ]; then echo unbound-shim-target >&2; exit 89; fi\nexec " + shquote.Quote(f.node) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shims, "node"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shims, "npm"), []byte("#!/bin/sh\necho wrong-shim-npm >&2\nexit 88\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{entrypoint.InstallOnlyEnv, "execute", "reentry"} {
		cmd := exec.Command(filepath.Join(e.LaunchDir(), "probe"))
		cmd.Env = append(os.Environ(), "HOME="+home, "NPM_CONFIG_PREFIX="+e.NpmPrefix, "PATH="+shims+":"+f.tools+":/bin:/usr/bin", "YOLO_INSTALL_ONLY=0", "YOLO_PACK_UPDATE=0")
		if mode == entrypoint.InstallOnlyEnv {
			cmd.Env = append(cmd.Env, mode+"=1")
		}
		if mode == "reentry" {
			cmd.Env = append(cmd.Env, "_YOLO_LAUNCHER_ACTIVE=:probe")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s consumer: %v\n%s", mode, err, out)
		}
		if mode != entrypoint.InstallOnlyEnv && strings.TrimSpace(string(out)) != "1.0.0" {
			t.Fatalf("target used another interpreter: %s", out)
		}
	}
	counts, _ := os.ReadFile(filepath.Join(f.root, "counts"))
	if strings.Count(string(counts), "install\n") != 1 || strings.Count(string(counts), "resolve\n") != 1 {
		t.Fatalf("consumer ran a mutable installer: %s", counts)
	}
}

func TestSelectedNPMGeneratedConsumerRequiresSameFilePATHNode(t *testing.T) {
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	for _, binding := range []string{"different-sibling", "relative", "colon-directory", "symlink-dotdot", "renamed-symlink", "renamed-hardlink"} {
		t.Run(binding, func(t *testing.T) {
			f := newSelectedNPMFixture(t)
			record := f.advance(t, false)
			for _, mode := range []string{entrypoint.InstallOnlyEnv, "execute", "warm-reentry"} {
				t.Run(mode, func(t *testing.T) {
					home, e := selectedNPMConsumer(t, f, record)
					if mode == "warm-reentry" {
						runSelectedConsumer(t, f, home, e, entrypoint.InstallOnlyEnv)
					}
					marker := filepath.Join(home, ".cache/yolo-program-captures/probe")
					before, _ := os.ReadFile(marker)
					dir := filepath.Join(home, "runtime")
					if binding == "colon-directory" {
						dir += ":other"
					}
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					physical := filepath.Join(dir, "node-real")
					if err := os.Link(f.node, physical); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(f.tools, "npm"), filepath.Join(dir, "npm")); err != nil {
						t.Fatal(err)
					}
					effect := filepath.Join(home, "intercepted-target")
					sibling := filepath.Join(dir, "node")
					switch binding {
					case "renamed-symlink", "colon-directory":
						if err := os.Symlink(physical, sibling); err != nil {
							t.Fatal(err)
						}
					case "renamed-hardlink":
						if err := os.Link(physical, sibling); err != nil {
							t.Fatal(err)
						}
					default:
						script := "#!/bin/sh\ncase \"$1\" in */probe) echo intercepted > " + shquote.Quote(effect) + "; echo INTERCEPTED; exit 0;; esac\nexec " + shquote.Quote(physical) + " \"$@\"\n"
						if err := os.WriteFile(sibling, []byte(script), 0o755); err != nil {
							t.Fatal(err)
						}
					}
					selection := physical
					if binding == "relative" {
						selection = filepath.Join("runtime", "node-real")
					}
					if binding == "symlink-dotdot" {
						checked := filepath.Join(home, "checked")
						if err := os.MkdirAll(checked, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(filepath.Join(dir, "sub"), filepath.Join(checked, "link")); err != nil {
							t.Fatal(err)
						}
						if err := os.Link(physical, filepath.Join(checked, "node")); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(filepath.Join(f.tools, "npm"), filepath.Join(checked, "npm")); err != nil {
							t.Fatal(err)
						}
						// Keep the claim raw: Join would erase the symlink/.. contradiction.
						selection = checked + "/link/../node-real"
						selected, err := os.Stat(selection)
						if err != nil {
							t.Fatal(err)
						}
						sibling, err := os.Stat(filepath.Join(filepath.Dir(selection), "node"))
						if err != nil || !os.SameFile(selected, sibling) {
							t.Fatal("fixture does not satisfy the old cleaned-directory check")
						}
					}
					selectors := t.TempDir()
					script := "#!/bin/sh\nexec " + shquote.Quote(physical) + " \"$@\"\n"
					if binding == "relative" || binding == "symlink-dotdot" {
						script = "#!/bin/sh\nprintf '%s\\n' " + shquote.Quote(selection) + "\n"
					}
					if err := os.WriteFile(filepath.Join(selectors, "node"), []byte(script), 0o755); err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(filepath.Join(e.LaunchDir(), "probe"))
					cmd.Dir = home
					cmd.Env = append(os.Environ(), "HOME="+home, "NPM_CONFIG_PREFIX="+e.NpmPrefix, "PATH="+selectors+":"+f.tools+":/bin:/usr/bin", "YOLO_INSTALL_ONLY=0", "YOLO_PACK_UPDATE=0")
					if mode == entrypoint.InstallOnlyEnv {
						cmd.Env = append(cmd.Env, mode+"=1")
					}
					if mode == "warm-reentry" {
						cmd.Env = append(cmd.Env, "_YOLO_LAUNCHER_ACTIVE=:probe")
					}
					out, err := cmd.CombinedOutput()
					valid := strings.HasPrefix(binding, "renamed-")
					if valid {
						if err != nil {
							t.Fatalf("same-file Node refused: %v\n%s", err, out)
						}
						lines := strings.Split(strings.TrimSpace(string(out)), "\n")
						if mode != entrypoint.InstallOnlyEnv && lines[len(lines)-1] != "1.0.0" {
							t.Fatalf("wrong target interpreter: %s", out)
						}
					} else {
						if err == nil || !strings.Contains(string(out), "prepared Node cannot be bound to PATH") || !strings.Contains(string(out), "retry a fresh launch") {
							t.Errorf("unsafe binding was not refused with a next step: %v\n%s", err, out)
						}
						if binding == "symlink-dotdot" && !strings.Contains(string(out), "canonical absolute Node path") {
							t.Errorf("noncanonical selection lacks its repair step: %s", out)
						}
						after, _ := os.ReadFile(marker)
						if string(before) != string(after) {
							t.Errorf("refused binding changed materialization marker: %s", out)
						}
						if len(before) == 0 {
							if _, err := os.Lstat(filepath.Join(e.NpmPrefix, "bin/probe")); !os.IsNotExist(err) {
								t.Error("refused binding materialized target")
							}
						}
						if _, err := os.Stat(effect); !os.IsNotExist(err) {
							t.Errorf("target intercepted: %s", out)
						}
						if strings.Contains(string(out), "1.0.0") {
							t.Errorf("refused binding executed target: %s", out)
						}
					}
				})
			}
			counts, _ := os.ReadFile(filepath.Join(f.root, "counts"))
			if strings.Count(string(counts), "install\n") != 1 || strings.Count(string(counts), "resolve\n") != 1 {
				t.Fatalf("consumer called installer: %s", counts)
			}
		})
	}
}

// Root is read-only here: execute only the PATH-binding expression extracted
// from the actual generator, not a /node runtime or a mocked materializer.
func TestSelectedNPMGeneratedRootDirectoryExpressionMatchesCheckedParent(t *testing.T) {
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	f := newSelectedNPMFixture(t)
	record := f.advance(t, false)
	_, e := selectedNPMConsumer(t, f, record)
	launcher, err := os.ReadFile(filepath.Join(e.LaunchDir(), "probe"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(launcher)
	end := strings.Index(text, "_CAPTURE_ID=")
	if end < 0 {
		t.Fatal("generated capture binding is absent")
	}
	boundary := "\nfi\n"
	start := strings.LastIndex(text[:end], boundary)
	if start < 0 {
		t.Fatal("generated validation boundary is absent")
	}
	binding := text[start+len(boundary) : end]
	cmd := exec.Command("/bin/bash", "-c", "_CAPTURE_NODE=/node; PATH=/bin:/usr/bin;\n"+binding+"printf '%s\\n' \"$PATH\"")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated root binding: %v\n%s", err, out)
	}
	if string(out) != "/:/bin:/usr/bin\n" {
		t.Fatalf("root binding differs from checked parent /: %q", out)
	}
}
