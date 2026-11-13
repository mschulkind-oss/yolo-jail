package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// brokeredskipped_test.go pins, through the launch's own config read and gate (and so `yolo
// config apply`'s, which runs both: liveapply.go), that a project broker request outside the
// user's ceiling is said, with `yolo allow <source> on_request <capability>` alone on its line
// and `yolo config apply` after it (docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins). Before the fix the owner of
// this repository approved `requests.github.runs` at the gate, was told nothing,
// and a later dispatch from the jail still asked.

func skippedBrokerOptions(t *testing.T) (*Options, *bytes.Buffer) {
	t.Helper()
	user := testconfig.User(t, "packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	ws := testconfig.Workspace(t, "[requests.github]\n"+
		"runs = [\"release-request.yml\", \"nightly-macos.yml\"]\n"+
		"approve_runs = [\"github-actions[bot]\"]\n")
	if err := os.MkdirAll(filepath.Join(ws.Root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/acme/repo.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	o := &Options{Workspace: ws.Root, Stdout: &buf, Stderr: &buf,
		IsTTYStdin: func() bool { return false }, IsTTYStdout: func() bool { return false },
		IsTTYStderr: func() bool { return false }, Getenv: os.Getenv}
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	return o, &buf
}

func lineIs(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func TestTheLaunchSaysABrokerRequestOutsideTheCeilingIsSkipped(t *testing.T) {
	o, buf := skippedBrokerOptions(t)
	merged, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatalf("a skipped request refused the launch:\n%s", buf.String())
	}
	got := buf.String()
	for _, want := range []string{"yolo allow github on_request runs", "yolo allow github on_request approve_runs", "yolo config apply"} {
		if !lineIs(got, want) {
			t.Fatalf("the launch's config read lacks the line %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "this project requests github runs release-request.yml, nightly-macos.yml.") {
		t.Fatalf("the launch does not name the request:\n%s", got)
	}

	// The gate, with no terminal to ask on, refuses showing the same lines.
	buf.Reset()
	if o.checkConfigChanges(merged, "podman") {
		t.Fatalf("an unapproved workspace config passed a non-interactive gate:\n%s", buf.String())
	}
	got = buf.String()
	for _, want := range []string{"Broker requests this project makes that your config skips:",
		"yolo allow github on_request runs", "yolo allow github on_request approve_runs", "yolo config apply"} {
		if !lineIs(got, want) {
			t.Fatalf("the gate's refusal lacks the line %q:\n%s", want, got)
		}
	}
}
