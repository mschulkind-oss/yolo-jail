package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"
	"testing"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestCommitHomeRefusesAGuestPeerBeforeAnyWrite (UG2): a running guest of another setup refuses a
// guest launch before the first write.
func TestCommitHomeRefusesAGuestPeerBeforeAnyWrite(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	makePeerSetup(t, ws, "seatbelt-self")
	holdKeeperKey(t, keeperKey(yoloruntime.JailName(ws, "seatbelt-self"), keeperNotchMacosUser))
	before := treeListing(t, ws)
	var stderr strings.Builder
	o := peerOptions(ws, "seatbelt-yolo-account", &stderr)
	if o.commitHome() {
		t.Fatal("commitHome committed beside a running guest peer")
	}
	want := "Refusing to launch: `seatbelt-self` is running for this workspace and shares its home with " +
		"`seatbelt-yolo-account`. Stop it with `yolo stop --setup seatbelt-self`, then launch again."
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("refusal text:\n%s", stderr.String())
	}
	if after := treeListing(t, ws); after != before {
		t.Errorf("the workspace changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCommitHomeLetsAGuestLaunchBesideARunningContainer(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	makePeerSetup(t, ws, "podman-self")
	holdKeeperKey(t, yoloruntime.JailName(ws, "podman-self"))
	var stderr strings.Builder
	o := peerOptions(ws, "seatbelt-self", &stderr)
	if !o.commitHome() || stderr.Len() != 0 {
		t.Fatalf("a guest beside a container must proceed silently: %q", stderr.String())
	}
}

func TestAGuestLaunchBesideItsOwnSetupIsNoPeer(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	makePeerSetup(t, ws, "seatbelt-self")
	holdKeeperKey(t, keeperKey(yoloruntime.JailName(ws, "seatbelt-self"), keeperNotchMacosUser))
	if !peerOptions(ws, "seatbelt-self", io.Discard).commitHome() {
		t.Fatal("a running session of the same setup refused its own launch")
	}
}

// TestRecordingAGuestSessionReleasesTheProjectLock (UHG-D4): the record is what a peer's jailStatus
// reads, so the lock is released right after it is written.
func TestRecordingAGuestSessionReleasesTheProjectLock(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	o := peerOptions(ws, "seatbelt-self", io.Discard)
	o.Getpid = os.Getpid
	defer o.releaseHomeLock()
	o.selectHome()
	if o.homeLock == nil || o.homeLock.isClosed() {
		t.Fatal("selectHome did not take the project lock")
	}
	key := keeperKey(yoloruntime.JailName(ws, "seatbelt-self"), keeperNotchMacosUser)
	o.macosUserKey = &macosUserKeying{key: key}
	o.recordKeyedSession(key, false)
	if !o.homeLock.isClosed() {
		t.Error("recording the session left the project lock held")
	}
	if got := liveKeyedSessions(key); len(got) != 1 {
		t.Errorf("live sessions = %d, want 1", len(got))
	}
}

// TestEveryGuestRecordPathReleasesTheLock: recordKeyedSession releases the lock itself, and the
// three guest paths are its only callers.
func TestEveryGuestRecordPathReleasesTheLock(t *testing.T) {
	if len(funcCalls(t, "keeperspawn.go", "recordKeyedSession")["releaseHomeLock"]) == 0 {
		t.Error("recordKeyedSession must release the project lock")
	}
	f, err := parser.ParseFile(token.NewFileSet(), "keeperspawn.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	callers := 0
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name.Name == "recordKeyedSession" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "recordKeyedSession" {
					callers++
				}
			}
			return true
		})
	}
	if callers != 3 {
		t.Errorf("recordKeyedSession has %d callers in keeperspawn.go, want the 3 guest paths", callers)
	}
}
