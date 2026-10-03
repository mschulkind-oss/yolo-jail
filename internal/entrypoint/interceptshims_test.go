package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// writePackWithIntercepts stages a one-pack YOLO_PACK_ROOT whose manifest carries the
// given contributions verbatim.
func writePackWithIntercepts(t *testing.T, name string, contributions ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` + strings.Join(contributions, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The kind renders into the BLOCK dir, first on PATH, so a bare name reaches the
// forwarder even for a program the image bakes at /bin. `sh` stands in for such a program
// here: it is at /bin on every backend this runs on.
func TestGenerateShimsWritesAnIntercept(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": writePackWithIntercepts(t, "fwd",
			`{"kind":"intercept","bin":"sh","forward":["yolo","gh","--"]}`),
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.BlockDir(), "sh"))
	if err != nil {
		t.Fatalf("no intercept shim in the block dir: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `exec yolo gh -- "$@"`) {
		t.Errorf("the shim does not exec the forwarder:\n%s", s)
	}
	// YOLO_BYPASS_SHIMS reaches the program behind the intercept, resolved on the
	// agent's PATH without the block dir.
	if !strings.Contains(s, `if [ -n "$YOLO_BYPASS_SHIMS" ]; then`) || !strings.Contains(s, "/sh") ||
		!strings.Contains(s, `sh' "$@"`) && !strings.Contains(s, `sh "$@"`) {
		t.Errorf("the shim's bypass does not exec the real sh:\n%s", s)
	}
	if strings.Contains(s, e.BlockDir()) {
		t.Errorf("the bypass resolved to the block dir itself, which is the shim:\n%s", s)
	}
}

// A blocker for the same name wins: a refusal is the more specific statement.
func TestABlockerWinsOverAnIntercept(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"YOLO_BLOCK_CONFIG": `[{"name":"yolo-fixture-cli","message":"no"}]`,
		"YOLO_PACK_ROOT": writePackWithIntercepts(t, "fwd",
			`{"kind":"intercept","bin":"yolo-fixture-cli","forward":["yolo","x","--"]}`),
	})
	var warned strings.Builder
	e.Stderr = &warned
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(e.BlockDir(), "yolo-fixture-cli"))
	if strings.Contains(string(body), "exec yolo x") || !strings.Contains(string(body), "exit 127") {
		t.Fatalf("the intercept replaced the blocker:\n%s", body)
	}
	if !strings.Contains(warned.String(), "blocked-tool entry for it wins") {
		t.Fatalf("warnings %q", warned.String())
	}
}

// The shim, run: arguments reach the forwarder unchanged, spaces and quotes included;
// with YOLO_BYPASS_SHIMS they reach the real program instead.
func TestInterceptShimRuns(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho \"real:$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fwd := filepath.Join(dir, "fwd")
	if err := os.WriteFile(fwd, []byte("#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "shim")
	if err := os.WriteFile(shim, []byte(InterceptShimContent("p", "x", real, []string{"fwd", "--"})), 0o755); err != nil {
		t.Fatal(err)
	}
	// The first run is the one WITHOUT the bypass, so the suite's own YOLO_BYPASS_SHIMS must
	// not reach it: AGENTS.md tells installers and scripts to set it, and a go test started
	// from one of them inherits it.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "YOLO_BYPASS_SHIMS=") {
			env = append(env, kv)
		}
	}
	env = append(env, "PATH="+dir+":/usr/bin:/bin")
	cmd := exec.Command(shim, "pr view", "it's")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "[--]\n[pr view]\n[it's]\n" {
		t.Fatalf("forwarded %q, %v", out, err)
	}
	cmd = exec.Command(shim, "a", "b")
	cmd.Env = append(env, "YOLO_BYPASS_SHIMS=1")
	out, err = cmd.CombinedOutput()
	if err != nil || string(out) != "real:a b\n" {
		t.Fatalf("bypass ran %q, %v", out, err)
	}
	// With nothing behind the intercept, the bypass says so rather than looping.
	none := filepath.Join(dir, "none")
	if err := os.WriteFile(none, []byte(InterceptShimContent("p", "x", "", []string{"fwd"})), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(none)
	cmd.Env = append(env, "YOLO_BYPASS_SHIMS=1")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no x is installed behind this intercept") {
		t.Fatalf("bypass with nothing behind: %q, %v", out, err)
	}
}

// BB-D67: an intercept lives in the block dir (OQ-BB8's ruling), so `type gh` answering
// ~/.yolo/bin/block/gh reads like a block to whoever is debugging. The shim's own text says
// what it is: not a blocker, whose forwarder, what it runs, and what is behind it.
func TestAnInterceptSaysItIsAForwarderNotABlocker(t *testing.T) {
	s := InterceptShimContent("github", "gh", "/bin/gh", []string{"yolo", "gh", "--"})
	for _, want := range []string{
		"# NOT A BLOCKER: this gh is pack github's forwarder (a yolo intercept).",
		"# It runs `yolo gh --` with the same arguments.",
		"intercepts as well as blocked tools",
		"# Behind it: /bin/gh, which YOLO_BYPASS_SHIMS=1 runs instead.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the shim lacks %q:\n%s", want, s)
		}
	}
	none := InterceptShimContent("github", "gh", "", []string{"yolo", "gh", "--"})
	if !strings.Contains(none, "# Behind it: no gh is installed, so YOLO_BYPASS_SHIMS=1 has nothing to run.") {
		t.Errorf("with nothing behind it:\n%s", none)
	}
	// Every comment line is one line, and the script still starts with its interpreter.
	if !strings.HasPrefix(s, "#!/bin/sh\n# NOT A BLOCKER") {
		t.Errorf("the header is not where a reader looks first:\n%s", s)
	}
}

// A value from a pack's manifest cannot break out of the shim.
func TestInterceptShimQuotesTheForwarder(t *testing.T) {
	s := InterceptShimContent("p\nrm -rf /", "x", "", []string{"fwd", "$(touch /pwn)", "a'b"})
	if strings.Contains(s, "\nrm -rf /") || !strings.Contains(s, `'$(touch /pwn)'`) {
		t.Fatalf("unquoted:\n%s", s)
	}
}
