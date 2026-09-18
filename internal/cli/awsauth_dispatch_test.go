package cli

import (
	"path/filepath"
	"testing"
)

// TestInternalDaemonDispatchRoutesAWSAuth pins the production caller the way its two
// siblings in openaiauth_dispatch_test.go do: by a return code only a real dispatch can
// produce.
//
// The discriminator is SHARPER here than in the openai-auth precedent, and that is worth
// keeping. A self-check with no `--settings` mints nothing, prints its graded line and
// returns 0; falling through the daemon switch reports an unknown daemon and returns 2. So
// success and dispatch-miss are different values, and deleting the `case "aws-auth":` row
// cannot leave this test green — where the openai-auth row's 1-vs-2 pair only holds
// because its self-check happens to fail on absent state.
//
// Verified empirically rather than reasoned from the flag set: the AWS self-check's exit
// codes were measured, not assumed.
func TestInternalDaemonDispatchRoutesAWSAuth(t *testing.T) {
	state := filepath.Join(t.TempDir(), "credentials.json")
	if rc := runInternalDaemon([]string{"aws-auth", "--self-check", "--state-file", state}); rc != 0 {
		t.Fatalf("aws-auth dispatch rc = %d, want self-check success 0", rc)
	}
}
