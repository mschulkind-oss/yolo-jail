package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `yolo host-run` typed in a jail runs nothing and prints the ready line with -C filled from
// YOLO_HOST_DIR. It fails if the early route in Main goes: the registry path prints the banner
// first and parses the global flags.
func TestMainRoutesHostRunBeforeTheFrontDoor(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/home/you/code/widgets")
	cwd := t.TempDir()
	t.Chdir(cwd)
	rc, out, errOut := runMainCaptured(t, "host-run", "--name", "t1", "--", "echo", "hi")
	if rc != 64 {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
	if want := "yolo host-run -C /home/you/code/widgets --name t1 -- echo hi"; !strings.Contains(errOut, want) {
		t.Fatalf("no ready line %q in:\n%s", want, errOut)
	}
	if !strings.Contains(errOut, "yolo wait t1") {
		t.Fatalf("missing wait line:\n%s", errOut)
	}
	if !strings.HasPrefix(errOut, "yolo host-run:") || !strings.Contains(errOut, "nothing ran here") {
		t.Fatalf("the refusal must come first, with no banner:\n%s", errOut)
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Fatalf("the in-jail form created %v", ents)
	}
}

func TestHostRunInJailWithoutHostDir(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "")
	rc, _, errOut := runMainCaptured(t, "host-run", "--", "printf", "=x")
	if rc != 64 {
		t.Fatalf("rc %d", rc)
	}
	if !strings.Contains(errOut, "  yolo host-run -- printf '=x'\n") || !strings.Contains(errOut, "terminal inside the workspace") {
		t.Fatalf("%s", errOut)
	}
	if !strings.Contains(errOut, "yolo host-run wait\n") {
		t.Fatalf("missing unnamed wait line:\n%s", errOut)
	}
}

func TestHostRunInJailPrintsNoLineFailingTheRuneCheck(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/h")
	rc, _, errOut := runMainCaptured(t, "host-run", "--", "echo", "a\u202eb")
	if rc != 64 || strings.Contains(errOut, "yolo host-run -C") || !strings.Contains(errOut, "U+202E") {
		t.Fatalf("rc %d %s", rc, errOut)
	}
}

func TestHostRunRefusesARedirectedStream(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	rc, out, errOut := runMainCaptured(t, "host-run", "--", "echo", "hi")
	if rc != 64 || !strings.Contains(errOut, "no redirect or pipe") || out != "" {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
}

func TestHostRunHelp(t *testing.T) {
	rc, out, errOut := runMainCaptured(t, "host-run", "--help")
	if rc != 0 || strings.TrimSpace(out) != strings.TrimSpace(hostRunUsage) || errOut != "" {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
	for _, want := range []string{"bare `yolo host-run`", "pipe", "no color", "pager"} {
		if !strings.Contains(hostRunUsage, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	// After `--`, --help is the command's.
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/h")
	rc, out, errOut = runMainCaptured(t, "host-run", "--", "ls", "--help")
	if rc != 64 || strings.Contains(out, "Usage: yolo host-run") || !strings.Contains(errOut, "-- ls --help") {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
}

func TestHelpListsHostRun(t *testing.T) {
	rc, out, _ := runMainCaptured(t, "--help")
	if rc != 0 || !strings.Contains(out, "host-run") {
		t.Fatalf("rc %d, help lacks host-run:\n%s", rc, out)
	}
}

func TestHostRunJailFormsNeedAWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)
	for _, verb := range []string{"wait", "show"} {
		rc, _, errOut := runMainCaptured(t, "host-run", verb)
		if rc != 64 || !strings.Contains(errOut, "no yolo workspace") || !strings.Contains(errOut, "-C") {
			t.Fatalf("%s: rc %d %s", verb, rc, errOut)
		}
	}
}

func TestHostRunShowWithNoRunsSaysSo(t *testing.T) {
	ws := evalTempDir(t)
	os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte("{}"), 0o644)
	sub := filepath.Join(ws, "deep", "er")
	os.MkdirAll(sub, 0o755)
	rc, _, errOut := runMainCaptured(t, "host-run", "show", "-C", sub)
	if rc != 64 || !strings.Contains(errOut, "no host-run results in this workspace yet") {
		t.Fatalf("rc %d %s", rc, errOut)
	}
	rc, _, errOut = runMainCaptured(t, "host-run", "wait", "--timeout", "bogus")
	if rc != 64 || !strings.Contains(errOut, "--timeout") {
		t.Fatalf("rc %d %s", rc, errOut)
	}
}

func evalTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResolveWorkspaceRootFrom(t *testing.T) {
	ws := evalTempDir(t)
	os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte("{}"), 0o644)
	sub := filepath.Join(ws, "a", "b")
	os.MkdirAll(sub, 0o755)
	if got, ok := resolveWorkspaceRootFrom(sub); !ok || got != ws {
		t.Fatalf("%q %v", got, ok)
	}
	// A home holding a workspace config is the credential boundary, never a workspace.
	home := evalTempDir(t)
	os.WriteFile(filepath.Join(home, "yolo-jail.jsonc"), []byte("{}"), 0o644)
	t.Setenv("HOME", home)
	if got, ok := resolveWorkspaceRootFrom(home); ok {
		t.Fatalf("the home resolved as a workspace: %q", got)
	}
	inside := filepath.Join(home, "code")
	os.MkdirAll(inside, 0o755)
	if got, ok := resolveWorkspaceRootFrom(inside); ok {
		t.Fatalf("the walk passed the home: %q", got)
	}
}
