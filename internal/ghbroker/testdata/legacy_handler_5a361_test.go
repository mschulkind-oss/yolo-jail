package ghbroker

// This source is overlaid as an additional test into the unmodified 5a361ba historical
// ghbroker package. Every production call below resolves against that commit's source.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestActualLegacyHandler5a361(t *testing.T) {
	dir := os.Getenv("YOLO_LEGACY_FIXTURE_DIR")
	if dir == "" {
		t.Fatal("fixture directory required")
	}
	b, _ := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	if code := b.ServeContext(context.Background(), writeReq(), jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed: %d", code)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("seed requests=%+v err=%v", requests, err)
	}
	grant, err := b.store.Decide(requests[0].ID, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	b.execute = func(_ context.Context, argv []string, stdin []byte, out, err func([]byte)) RunResult {
		writes.Add(1)
		if e := os.WriteFile(filepath.Join(dir, "synthetic-write"), []byte(fmt.Sprint(argv)), 0600); e != nil {
			t.Error(e)
		}
		return RunResult{Exit: 0}
	}
	var frames atomic.Int32
	endpointDir := filepath.Base(paths.HostServicesDir(authorityContainer, false))
	endpoint := filepath.Join(dir, endpointDir, "github-broker.endpoint")
	if err := os.MkdirAll(filepath.Dir(endpoint), 0700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "legacy.sock")
	stop := make(chan struct{})
	finishedDaemon, finishedFront := make(chan error, 1), make(chan error, 1)
	go func() {
		finishedDaemon <- hostservice.ServeFrontedUnix(func(s *hostservice.Session) {
			decoded := requestFrom(s)
			if frames.Add(1) == 2 && (len(decoded.Argv) != 0 || decoded.Mode != "" || decoded.Operation != "" || decoded.Invalid != "") {
				t.Errorf("rotated frame decoded as act/status: %+v", decoded)
			}
			b.Handle(s)
		}, sock, stop)
	}()
	defer func() { close(stop); <-finishedDaemon; <-finishedFront }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("historical daemon socket unavailable")
		}
	}
	go func() { finishedFront <- svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, stop) }()
	for deadline := time.Now().Add(5 * time.Second); !svcendpoint.Probe(endpoint); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("historical TLS front unavailable")
		}
	}
	// This endpoint's handler and ServeContext are both the real 5a361ba source.
	if err := os.WriteFile(filepath.Join(dir, "endpoint"), []byte(endpoint), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out awaiting both framed calls")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, grants, err := b.store.List()
	if err != nil || frames.Load() != 2 || len(grants) != 1 || grants[0].ID != grant.ID || grants[0].Uses != 0 || writes.Load() != 1 {
		t.Fatalf("actual historical handler filed/spent/executed on rotated frame: grants=%+v writes=%d err=%v", grants, writes.Load(), err)
	}
	requests, _, err = b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("actual historical handler filed rotated frame: requests=%+v err=%v", requests, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthetic-write")); err != nil {
		t.Fatalf("positive control did not write fake artifact: %v", err)
	}
}
