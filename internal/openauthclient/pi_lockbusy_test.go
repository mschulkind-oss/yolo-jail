package openauthclient

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// A running pi holding auth.json.lock past pi's stale window is not a missing login, so the
// client reports it with its own exit status, ExitPiAuthLockBusy, and the launcher starts pi
// instead of a browser login. Every other failure keeps its status: a writer failure of any
// other kind exits 1, and a service that exits with the same number is folded to 1 so the
// status stays unambiguous.
func TestRunTokenReportsAHeldPiLockWithItsOwnExitStatus(t *testing.T) {
	saved := piAuthLockRule
	piAuthLockRule = piLockRule{stale: 30 * time.Second, wait: 200 * time.Millisecond}
	t.Cleanup(func() { piAuthLockRule = saved })

	run := func(t *testing.T, authPath string, serve hostservice.Handler) (int, string) {
		t.Helper()
		endpoint := clientEndpoint(t, serve)
		var stdout, stderr bytes.Buffer
		rc := Run([]string{"token", "--pi-auth", authPath}, func(name string) string {
			if name == EndpointEnv {
				return endpoint
			}
			return ""
		}, &stdout, &stderr)
		return rc, stderr.String()
	}
	validView := func(s *hostservice.Session) {
		_ = s.JSON(map[string]any{"access_token": "access", "expires_at": int64(4_102_444_800_000), "generation": int64(3)})
		s.Exit(0)
	}

	t.Run("lock held by a running pi", func(t *testing.T) {
		authPath := filepath.Join(t.TempDir(), "auth.json")
		if err := os.Mkdir(authPath+".lock", 0o700); err != nil {
			t.Fatal(err)
		}
		rc, stderr := run(t, authPath, validView)
		if rc != ExitPiAuthLockBusy {
			t.Fatalf("rc = %d, want ExitPiAuthLockBusy (%d); stderr=%q", rc, ExitPiAuthLockBusy, stderr)
		}
		if !strings.Contains(stderr, "held by a running pi") {
			t.Fatalf("stderr does not name the holder: %q", stderr)
		}
	})
	t.Run("any other writer failure", func(t *testing.T) {
		rc, stderr := run(t, filepath.Join(t.TempDir(), "auth.json"), func(s *hostservice.Session) {
			_ = s.JSON(map[string]any{"access_token": "access"})
			s.Exit(0)
		})
		if rc != 1 {
			t.Fatalf("rc = %d, want 1; stderr=%q", rc, stderr)
		}
	})
	t.Run("a service exiting with the same number", func(t *testing.T) {
		rc, stderr := run(t, filepath.Join(t.TempDir(), "auth.json"), func(s *hostservice.Session) {
			s.Exit(ExitPiAuthLockBusy)
		})
		if rc != 1 {
			t.Fatalf("rc = %d, want 1: only the pi writer may report a held lock; stderr=%q", rc, stderr)
		}
	})
}

// The lock-timeout error is a PiAuthLockBusyError, which is what the client maps to
// ExitPiAuthLockBusy. A plain error with the same text would exit 1 and send the user to a login.
func TestAcquirePiAuthLockTimeoutIsAPiAuthLockBusyError(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.Mkdir(authPath+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := acquirePiAuthLock(authPath, piLockRule{stale: 30 * time.Second, wait: 100 * time.Millisecond})
	if err == nil {
		release()
		t.Fatal("acquired a lock a live pi holds")
	}
	var busy *PiAuthLockBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("error %q (%T) is not a PiAuthLockBusyError", err, err)
	}
}
