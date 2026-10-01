package entrypoint

// callertokenfiles_test.go pins the in-jail caller-token FILES (paths.JailCallerTokenDir;
// docs/plans/notch-convergence.md §2.3): the boot writes each token it was handed to a 0600 file
// of its own. The aws-auth pack named that file as AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE until
// its token was SCOPED (docs/reference/providers.md OQ-CN7 (c)): its pointer now
// names the token itself, delivered only in the selecting agent's env file, and the boot is
// handed no aws-auth token to write a file for.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
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

// THE AWS POINTER NAMES THE SCOPED TOKEN, NOT A FILE (OQ-CN7 (c)): the AWS SDKs send
// AWS_CONTAINER_AUTHORIZATION_TOKEN as `Authorization` only when no
// AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE is set, so the pointer must carry the first and never the
// second, and the value is the `{caller_token}` the launch resolves per agent.
func TestTheAWSPointerNamesTheScopedTokenNotAFile(t *testing.T) {
	closure := testPacksForAgent(t, "claude", "aws-auth")
	env := launchEnvFor(t, closure, map[string]string{"claude": "bedrock"}, "claude")
	if env["AWS_CONTAINER_AUTHORIZATION_TOKEN"] != loopholedecl.TokenCallerToken {
		t.Errorf("AWS_CONTAINER_AUTHORIZATION_TOKEN = %q, want %q beside the credentials URI %q",
			env["AWS_CONTAINER_AUTHORIZATION_TOKEN"], loopholedecl.TokenCallerToken,
			env["AWS_CONTAINER_CREDENTIALS_FULL_URI"])
	}
	if v, ok := env["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"]; ok {
		t.Errorf("the pointer still names a token file (%q), which the SDK prefers over the token", v)
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
