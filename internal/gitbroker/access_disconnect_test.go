package gitbroker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestGitAccessClientDisconnectStopsPausedHostSSHReadLeg(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "ssh.pid")
	ready := filepath.Join(dir, "child-ready")
	gatePath := filepath.Join(dir, "child-gate")
	marker := filepath.Join(dir, "child-survived")
	if err := syscall.Mkfifo(gatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	// Keeping both ends open prevents an EOF: the child stays blocked on read until
	// the client disconnects, regardless of scheduler or census load.
	gate, err := os.OpenFile(gatePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	f := newFixture(t, fmt.Sprintf("(printf ready > %q; IFS= read -r token < %q && echo survived > %q) &\nchild=$!\nprintf '%%s %%s\\n' \"$$\" \"$child\" > %q\nwait \"$child\"", ready, gatePath, marker, pidfile))
	old := f.upstreamRef("refs/heads/main")
	f.commit("disconnect")
	proposal := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	conn, err := svcendpoint.Dial(f.endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(map[string]any{"access": map[string]any{"op": "evaluate", "version": 1, "proposal": proposal}})
	if err != nil {
		t.Fatal(err)
	}
	if err := frameproto.WriteRequest(conn, wire); err != nil {
		t.Fatal(err)
	}
	var shellPID, childPID int
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		b, err := os.ReadFile(pidfile)
		if err == nil {
			if _, err = fmt.Sscanf(strings.TrimSpace(string(b)), "%d %d", &shellPID, &childPID); err == nil {
				break
			}
		}
	}
	if shellPID == 0 || childPID == 0 || shellPID == childPID {
		_ = conn.Close()
		t.Fatalf("host SSH shell and child did not start: shell=%d child=%d", shellPID, childPID)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !os.IsNotExist(err) {
			_ = conn.Close()
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(ready); err != nil {
		_ = conn.Close()
		t.Fatalf("SSH child did not reach its blocking read: %v", err)
	}
	shellStart, shellLive, shellErr := processIdentity(shellPID)
	childStart, childLive, childErr := processIdentity(childPID)
	if shellErr != nil || childErr != nil || !shellLive || !childLive {
		_ = conn.Close()
		t.Fatalf("cannot capture live SSH process identities: %q %q, %v %v", shellStart, childStart, shellErr, childErr)
	}
	_ = conn.Close()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		shellNow, shellRunning, shellErr := processIdentity(shellPID)
		childNow, childRunning, childErr := processIdentity(childPID)
		if shellErr != nil || childErr != nil {
			t.Fatalf("cannot inspect SSH shell and child: %v %v", shellErr, childErr)
		}
		if (!shellRunning || shellNow != shellStart) && (!childRunning || childNow != childStart) {
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("host SSH descendant survived the actual client disconnect")
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("host SSH shell %d or child %d survived the actual client disconnect", shellPID, childPID)
}
