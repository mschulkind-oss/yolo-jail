package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// guardFixture is a workspace with a .yolo (homes, a launch log, a durable dir), a fake real
// mv and rm that only record their argv, and the guard shims generated through GenerateShims
// in front of them.
type guardFixture struct {
	t        *testing.T
	ws       string
	state    string
	durable  string
	blockDir string
	record   string
}

func newGuardFixture(t *testing.T) *guardFixture {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &guardFixture{t: t, ws: filepath.Join(root, "ws"), record: filepath.Join(root, "ran")}
	f.state = filepath.Join(f.ws, ".yolo")
	f.durable = filepath.Join(f.state, "durable")
	for _, d := range []string{filepath.Join(f.state, "home"), filepath.Join(f.durable, "wt"), filepath.Join(f.ws, "dst")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(f.state, "launch.log"), filepath.Join(f.ws, "a")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "realbin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"mv", "rm"} {
		body := "#!/bin/sh\nprintf '%s\\n' \"" + tool + " $*\" > " + f.record + "\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME": filepath.Join(root, "home"),
		"YOLO_BLOCK_CONFIG": `[{"name":"mv","guard":"workspace-state","message":"m"},` +
			`{"name":"rm","guard":"workspace-state","message":"r"}]`,
	})
	e.Workspace = f.ws
	e.ShimBinDir = bin
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	f.blockDir = e.BlockDir()
	return f
}

// run runs the guard for tool in dir with argv, and reports the exit code, stderr, and the
// argv the real tool received ("" when it never ran).
func (f *guardFixture) run(dir string, env []string, tool string, argv ...string) (int, string, string) {
	f.t.Helper()
	return f.runIn("sh", dir, env, tool, argv...)
}

// guardShells are the shells each case runs under: sh, and dash when it is on PATH, since a
// guard is a POSIX script that macOS's sh (bash) and Debian's (dash) both run.
func guardShells() []string {
	shells := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	}
	return shells
}

func (f *guardFixture) runIn(shell, dir string, env []string, tool string, argv ...string) (int, string, string) {
	f.t.Helper()
	_ = os.Remove(f.record)
	sh, err := exec.LookPath(shell)
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(sh, append([]string{filepath.Join(f.blockDir, tool)}, argv...)...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=/bin:/usr/bin"}, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	rc := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			f.t.Fatalf("run %s %v: %v", tool, argv, err)
		}
		rc = ee.ExitCode()
	}
	ran, _ := os.ReadFile(f.record)
	return rc, stderr.String(), strings.TrimSpace(string(ran))
}

func (f *guardFixture) refuses(dir, tool string, argv ...string) {
	f.t.Helper()
	for _, shell := range guardShells() {
		rc, stderr, ran := f.runIn(shell, dir, nil, tool, argv...)
		if rc != 1 || ran != "" {
			f.t.Errorf("%s: %s %v in %s: rc=%d ran=%q, want a refusal (rc 1, real %s not run); stderr:\n%s",
				shell, tool, argv, dir, rc, ran, tool, stderr)
			continue
		}
		for _, want := range []string{tool + ": refusing ", "yolo --fresh-home", "YOLO_BYPASS_SHIMS=1",
			f.state + " holds this workspace's agent homes", "Your own files under " + f.durable} {
			if !strings.Contains(stderr, want) {
				f.t.Errorf("%s: %s %v: the refusal lacks %q:\n%s", shell, tool, argv, want, stderr)
			}
		}
	}
}

func (f *guardFixture) passes(dir, tool string, argv ...string) {
	f.t.Helper()
	for _, shell := range guardShells() {
		rc, stderr, ran := f.runIn(shell, dir, nil, tool, argv...)
		if want := strings.TrimSpace(tool + " " + strings.Join(argv, " ")); rc != 0 || ran != want {
			f.t.Errorf("%s: %s %v in %s: rc=%d ran=%q, want the real %s run with the same argv; stderr:\n%s",
				shell, tool, argv, dir, rc, ran, tool, stderr)
		}
	}
}

func TestGuardRmRefusesWorkspaceStateAndPassesTheRest(t *testing.T) {
	f := newGuardFixture(t)
	j := filepath.Join
	// Refusals: .yolo, inside it, the durable dir itself, and every directory holding .yolo.
	f.refuses(f.ws, "rm", "-rf", f.state)
	f.refuses(f.ws, "rm", "-rf", j(f.state, "home"))
	f.refuses(f.ws, "rm", "-f", j(f.state, "launch.log"))
	f.refuses(f.ws, "rm", "-rf", f.durable)
	f.refuses(f.ws, "rm", "-rf", f.ws)
	f.refuses(f.ws, "rm", "-rf", filepath.Dir(f.ws))
	f.refuses(f.ws, "rm", "-rf", "/")
	f.refuses(f.ws, "rm", "-rf", f.state+"/")
	// `--` ends the options; a later operand is still judged.
	f.refuses(f.ws, "rm", "-rf", "--", j(f.ws, "a"), j(f.state, "home"))
	// Relative paths, resolved against the working directory.
	f.refuses(f.ws, "rm", "-r", ".yolo")
	f.refuses(f.ws, "rm", "-rf", ".")
	f.refuses(f.state, "rm", "-r", "home")
	f.refuses(j(f.ws, "dst"), "rm", "-rf", "..")
	f.refuses(j(f.ws, "dst"), "rm", "-rf", "../.yolo/home")

	// Pass-throughs: the project, and the durable dir's contents.
	f.passes(f.ws, "rm", "-f", j(f.ws, "a"))
	f.passes(f.ws, "rm", "-rf", "dst")
	f.passes(f.ws, "rm", "-rf", j(f.durable, "wt"))
	f.passes(f.durable, "rm", "-rf", "wt")
	f.passes(f.ws, "rm", "-rf", ".yolo/durable/wt")
	// An option-looking operand after `--` is a file, judged as one.
	f.passes(f.ws, "rm", "--", "-f")
}

func TestGuardResolvesTheParentNeverTheLinkNamed(t *testing.T) {
	f := newGuardFixture(t)
	root := filepath.Dir(f.ws)
	// A symlinked PARENT is resolved: the path reaches .yolo/home.
	link := filepath.Join(root, "wslink")
	if err := os.Symlink(f.ws, link); err != nil {
		t.Fatal(err)
	}
	f.refuses(root, "rm", "-rf", filepath.Join(link, ".yolo", "home"))
	f.refuses(root, "rm", "-rf", "wslink/.yolo")
	// A link NAMED as the argument is judged as the link, which rm removes alone.
	f.passes(root, "rm", "-rf", "wslink")
	if err := os.Symlink(filepath.Join(f.state, "home"), filepath.Join(f.ws, "homelink")); err != nil {
		t.Fatal(err)
	}
	f.passes(f.ws, "rm", "homelink")
	// A trailing slash goes through the link, as rm itself does.
	f.refuses(f.ws, "rm", "-rf", "homelink/")
}

func TestGuardMvJudgesSourcesAndWhereTheyLand(t *testing.T) {
	f := newGuardFixture(t)
	j := filepath.Join
	// Sources inside .yolo, or holding it.
	f.refuses(f.ws, "mv", f.state, j(f.ws, "old"))
	f.refuses(f.ws, "mv", ".yolo/home", "dst")
	f.refuses(f.ws, "mv", "-t", "dst", ".yolo")
	f.refuses(f.ws, "mv", "--target-directory="+j(f.ws, "dst"), j(f.state, "home"))
	f.refuses(f.ws, "mv", "--target-directory", "dst", ".yolo/home")
	f.refuses(filepath.Dir(f.ws), "mv", "ws", "ws2")
	// Landing: overwriting something inside .yolo, or replacing .yolo or a parent of it.
	f.refuses(f.ws, "mv", "a", ".yolo/launch.log")
	f.refuses(f.ws, "mv", "-T", "dst", ".yolo/home")
	if err := os.MkdirAll(j(f.ws, "dst", ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.refuses(j(f.ws, "dst"), "mv", ".yolo", f.ws) // would replace the workspace's .yolo
	if err := os.WriteFile(j(f.state, "home", "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.refuses(f.ws, "mv", "-t", ".yolo/home", "a")

	// Passes: ordinary moves, the durable dir's contents, and a new name inside .yolo.
	f.passes(f.ws, "mv", "a", "b")
	f.passes(f.ws, "mv", j(f.durable, "wt"), j(f.ws, "wt"))
	f.passes(f.ws, "mv", "a", ".yolo/durable/a")
	f.passes(f.ws, "mv", "a", ".yolo/newname")
	f.passes(f.ws, "mv", "dst", ".yolo") // lands as .yolo/dst, which does not exist
	// The destination is the workspace or a parent of it, which only receives a new name: the
	// option parser must not mistake `-t`'s or the last operand's value for a source.
	f.passes(j(f.ws, "dst"), "mv", "-t", f.ws, "x")
	f.passes(j(f.ws, "dst"), "mv", "-fvt", "..", "x")
	f.passes(j(f.ws, "dst"), "mv", "x", "..")
	f.passes(f.ws, "mv", "dst/x", ".")
	// `-S`'s value is a suffix, not a path.
	f.passes(f.ws, "mv", "-S", ".yolo", "a", "b")
	f.passes(f.ws, "mv", "--suffix", ".yolo", "a", "b")
}

func TestGuardBypassVariableRunsTheRealTool(t *testing.T) {
	f := newGuardFixture(t)
	rc, _, ran := f.run(f.ws, []string{"YOLO_BYPASS_SHIMS=1"}, "rm", "-rf", f.state)
	if rc != 0 || ran != "rm -rf "+f.state {
		t.Errorf("YOLO_BYPASS_SHIMS=1 rm -rf .yolo: rc=%d ran=%q, want the real rm run", rc, ran)
	}
}

// TestGuardrailsPackShipsMvAndRmGuards pins the whole chain from the shipped manifest to a
// rendered guard: packload carries `guard`, config's normalization keeps it, and the renderer
// writes a guard (not a plain block) for each.
func TestGuardrailsPackShipsMvAndRmGuards(t *testing.T) {
	var guardrails *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "guardrails" {
			guardrails = p
		}
	}
	if guardrails == nil {
		t.Fatal("no embedded guardrails pack")
	}
	entries := config.NormalizeBlockedToolsWith(nil, packload.BlockedTools([]*packload.Pack{guardrails}))
	scripts := RenderBlockers(entries, BlockerRender{
		LookPath:  func(bin string) string { return "/bin/" + bin },
		RealBin:   func(name string) (string, bool) { return "/bin/" + name, true },
		Workspace: "/workspace",
	})
	got := map[string]string{}
	for _, s := range scripts {
		got[s.Name] = s.Content
	}
	for _, tool := range []string{"mv", "rm"} {
		body := got[tool]
		if !strings.Contains(body, "_yg_ws=/workspace") || !strings.Contains(body, "exec /bin/"+tool+` "$@"`) ||
			!strings.Contains(body, "yolo --fresh-home") {
			t.Errorf("the guardrails pack's %s is not rendered as a guard over /workspace:\n%s", tool, body)
		}
	}
	for _, e := range entries {
		m := e.(*jsonx.OrderedMap)
		if n, _ := m.Get("name"); n == "mv" || n == "rm" {
			if msg, _ := m.Get("message"); msg == nil || msg == "" {
				t.Errorf("%v carries no message for the briefing to list", n)
			}
		}
	}
}

func TestRenderGuardSkipsWhatItCannotRender(t *testing.T) {
	for _, tc := range []struct {
		name, entry, workspace, want string
	}{
		{"no workspace", `{"name":"rm","guard":"workspace-state"}`, "", "no workspace"},
		{"unknown path set", `{"name":"rm","guard":"elsewhere"}`, "/w", "does not know"},
		{"unknown tool", `{"name":"cp","guard":"workspace-state"}`, "/w", "knows those of mv and rm only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := jsonx.Decode([]byte("[" + tc.entry + "]"))
			if err != nil {
				t.Fatal(err)
			}
			var warned []string
			scripts := RenderBlockers(decoded.([]any), BlockerRender{
				RealBin:   func(name string) (string, bool) { return "/bin/" + name, true },
				Workspace: tc.workspace,
				Warn:      func(s string) { warned = append(warned, s) },
			})
			if len(scripts) != 0 {
				t.Errorf("rendered %d scripts, want none: an unrenderable guard must not become a plain block", len(scripts))
			}
			if len(warned) != 1 || !strings.Contains(warned[0], tc.want) {
				t.Errorf("warnings %q, want one naming %q", warned, tc.want)
			}
		})
	}
}

// TestJailRealBinFindsMvOffTheShimBinDir pins macOS's case: the shim bin dir (/usr/bin there)
// has no mv, so the guard passes calls to the first mv on the agent's PATH outside yolo's dirs.
func TestJailRealBinFindsMvOffTheShimBinDir(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "bin")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "mv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": filepath.Join(root, "home")})
	e.ShimBinDir = filepath.Join(root, "empty")
	if err := os.MkdirAll(e.BlockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.BlockDir(), "mv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := jailRealBin(e, e.BlockDir()+":"+other, "mv")
	if !ok || got != filepath.Join(other, "mv") {
		t.Errorf("jailRealBin = %q, %v; want %q (never the block dir's own shim)", got, ok, filepath.Join(other, "mv"))
	}
}
