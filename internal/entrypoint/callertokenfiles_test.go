package entrypoint

// callertokenfiles_test.go pins the in-jail caller-token FILES (paths.JailCallerTokenDir;
// docs/plans/notch-convergence.md §2.3): the boot writes each token it was handed to a 0600 file
// of its own, and the aws-auth pack names that file as AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE,
// the slot the AWS SDKs' container-credentials provider reads and sends as `Authorization`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestTheBootWritesEachCallerTokenToAPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "caller-tokens")
	prev := callerTokenDir
	callerTokenDir = dir
	t.Cleanup(func() { callerTokenDir = prev })

	tok := strings.Repeat("c3", 32)
	awsVar := paths.ServiceCallerTokenEnv("aws-auth")
	e, stderr, _ := loudEnv(t)
	e.Vars[awsVar] = tok
	e.Vars[paths.ServiceCallerTokenEnv("broken")] = "local"
	e.Vars["UNRELATED_TOKEN"] = tok
	writeCallerTokenFiles(e)

	got, err := os.ReadFile(filepath.Join(dir, awsVar))
	if err != nil {
		t.Fatalf("no token file for %s: %v (stderr %q)", awsVar, err, stderr)
	}
	if string(got) != tok {
		t.Errorf("token file = %q, want the token alone, no newline", got)
	}
	info, _ := os.Stat(filepath.Join(dir, awsVar))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %o, want 600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("token dir holds %d entries, want only the well-formed caller token's", len(entries))
	}
	// An entry's boot writes the same bytes again (an attach reuses the running jail's token).
	writeCallerTokenFiles(e)
	if again, _ := os.ReadFile(filepath.Join(dir, awsVar)); string(again) != tok {
		t.Errorf("a second boot rewrote the file as %q", again)
	}
}

// The file the pack names IS the file the boot writes: one path, composed by paths on one side
// and spelled literally in packs/aws-auth/pack.json on the other, so a drift fails here rather
// than as an SDK sending an empty Authorization.
func TestTheAWSPointerNamesTheBootsTokenFile(t *testing.T) {
	closure := testPacksForAgent(t, "claude", "aws-auth")
	env := packload.EnvVarsFor(closure, map[string]string{"claude": "bedrock"}, "claude")
	want := paths.JailCallerTokenFile(paths.ServiceCallerTokenEnv("aws-auth"))
	if env["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"] != want {
		t.Errorf("AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE = %q, want %q beside the credentials URI %q",
			env["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"], want, env["AWS_CONTAINER_CREDENTIALS_FULL_URI"])
	}
	if env["AWS_CONTAINER_CREDENTIALS_FULL_URI"] == "" {
		t.Error("the bedrock profile no longer delivers the credentials URI, so this test proves nothing")
	}
}

// The call site: the container boot writes the files before it starts the supervisor, so a
// daemon's client never races an absent file. The step's own body is run, so a step that
// stopped calling writeCallerTokenFiles fails here as well as a step that moved.
func TestTheBootWritesCallerTokenFilesBeforeStartingTheSupervisor(t *testing.T) {
	assertStepBefore(t, bootContainer, "write_caller_token_files", "start_jail_daemon_supervisor",
		"a daemon's client could read an absent token file and be refused")
	dir := filepath.Join(t.TempDir(), "caller-tokens")
	prev := callerTokenDir
	callerTokenDir = dir
	t.Cleanup(func() { callerTokenDir = prev })
	tok := strings.Repeat("d4", 32)
	awsVar := paths.ServiceCallerTokenEnv("aws-auth")
	e, _, _ := loudEnv(t)
	e.Vars[awsVar] = tok
	mustBootStep(t, "write_caller_token_files").run(&bootRun{e: e, target: bootContainer})
	if got, err := os.ReadFile(filepath.Join(dir, awsVar)); err != nil || string(got) != tok {
		t.Fatalf("the write_caller_token_files step wrote %q (%v), want the token: an AWS SDK would "+
			"read no token and be refused", got, err)
	}
}
