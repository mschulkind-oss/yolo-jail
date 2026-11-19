package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

var scopedDispatchPipe = os.Pipe

func dispatchScopedPackCapture(t *testing.T, args []string) (rc int, stdout, stderr string) {
	t.Helper()
	outR, outW, err := scopedDispatchPipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := scopedDispatchPipe()
	if err != nil {
		outR.Close()
		outW.Close()
		t.Fatal(err)
	}
	type readResult struct {
		data []byte
		err  error
	}
	drain := func(r *os.File) <-chan readResult {
		done := make(chan readResult, 1)
		go func() {
			data, err := io.ReadAll(r)
			done <- readResult{data, err}
		}()
		return done
	}
	outDone, errDone := drain(outR), drain(errR)
	oldOut, oldErr, oldHook := os.Stdout, os.Stderr, updateHook
	os.Stdout, os.Stderr = outW, errW
	updateHook = func(string, []string) (int, bool) { return 0, false }
	defer func() {
		os.Stdout, os.Stderr, updateHook = oldOut, oldErr, oldHook
		outW.Close()
		errW.Close()
		out, errout := <-outDone, <-errDone
		outR.Close()
		errR.Close()
		stdout, stderr = string(out.data), string(errout.data)
		if out.err != nil || errout.err != nil {
			t.Errorf("dispatch capture: %v %v", out.err, errout.err)
		}
	}()
	rc = dispatchNative("pack", append([]string{"pack"}, args...))
	return
}

func TestDispatchScopedPackAndIndexedEditUseBoundUserTransaction(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "# owned\npacks=[]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(project.Root)
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"a", "b"} {
		scopedCLIWrite(t, filepath.Join(root, "raw", name, "SKILL.md"), name+"\n")
	}
	rc, out, errw := dispatchScopedPackCapture(t, []string{"add", "local/corpus", "--skills-dir", "raw", "--only", "a"})
	if rc != 0 || !strings.Contains(out, "Added") {
		t.Fatalf("native pack add: %d %s %s", rc, out, errw)
	}
	rc, out, errw = dispatchConfigCapture(t, []string{"set", "packs[0].skills.only[0]", `"b"`, "--user"})
	if rc != 0 || !strings.Contains(out, "Written.") || !strings.Contains(out, "valid") {
		t.Fatalf("native edit/post-write validation: %d %s %s", rc, out, errw)
	}
	rc, out, errw = dispatchConfigCapture(t, []string{"why", "packs[0].skills.only[0]"})
	if rc != 0 || !strings.Contains(out, `"b"`) || !strings.Contains(out, "config.toml:") {
		t.Fatalf("native why: %d %s %s", rc, out, errw)
	}
	rc, out, errw = dispatchScopedPackCapture(t, []string{"explain", "local/corpus"})
	if rc != 0 || !strings.Contains(out, "selected: [b]") {
		t.Fatalf("native explain: %d %s %s", rc, out, errw)
	}
	rc, out, errw = dispatchScopedPackCapture(t, []string{"remove", "local/corpus", "--skills-dir", "raw"})
	if rc != 0 || !strings.Contains(out, "Removed") {
		t.Fatalf("native remove: %d %s %s", rc, out, errw)
	}
	after, _ := os.ReadFile(user.Path)
	if !strings.Contains(string(after), "# owned") || strings.Contains(string(after), "local/corpus") {
		t.Fatalf("native transaction lost comment/kept tuple: %s", after)
	}
}

func TestPackScopedOptionsRefuseLegacyWithoutAddingSyntax(t *testing.T) {
	project := testconfig.Workspace(t, "")
	original := "// legacy remains\n{\"packs\": []}\n"
	user := testconfig.UserFormat(t, configdoc.JSONC, original)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	scopedCLIWrite(t, filepath.Join(filepath.Dir(user.Path), "packs/corpus/skills/a/SKILL.md"), "a\n")
	rc, _, errw := runPackCapture(t, project.Root, []string{"add", "local/corpus", "--only", "a"})
	if rc == 0 || !strings.Contains(errw, "yolo config migrate") {
		t.Fatalf("legacy table option accepted: %d %s", rc, errw)
	}
	before, _ := os.ReadFile(user.Path)
	if string(before) != original {
		t.Fatal("legacy refusal wrote")
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"add", "claude", "--dry-run"})
	if rc != 0 || errw != "" || !strings.Contains(out, "Dry run") {
		t.Fatalf("legacy string add changed: %d %s %s", rc, out, errw)
	}
	after, _ := os.ReadFile(user.Path)
	if string(after) != original {
		t.Fatal("legacy dry-run wrote")
	}
}
