package perf

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

func traceStoreFixture(t *testing.T) (*TraceStore, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, private := filepath.Join(base, "traces"), filepath.Join(base, "private")
	s, err := OpenTraceStore(root, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root, private
}

func storeMeta(n int, start int64) TraceMetadata {
	return TraceMetadata{Trace: fmt.Sprintf("%032x", n), Kind: "launch", Start: start, Workspace: "ab12", Version: "test", Commit: "test"}
}

func storeMint(t *testing.T, s *TraceStore, n int, start int64) *TraceClaim {
	t.Helper()
	c, err := s.Mint(storeMeta(n, start))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func storeWrite(t *testing.T, c *TraceClaim, name string, p []byte) *TraceWriter {
	t.Helper()
	w, err := c.CreateWriter(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if n, err := w.Write(p); err != nil || n != len(p) {
		t.Fatalf("write %d/%d: %v", n, len(p), err)
	}
	return w
}

func assertTraceStoreClosePrivacy(t *testing.T, err error, roots ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("failed owned descriptor close was reported successful")
	}
	for _, root := range roots {
		if strings.Contains(err.Error(), root) {
			t.Error("close error exposed an owned fixture locator")
		}
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		t.Error("close error retained an extractable PathError")
	}
	if errors.Unwrap(err) != nil {
		t.Error("close error retained an underlying cause")
	}
	if _, ok := err.(interface{ Unwrap() []error }); ok {
		t.Error("close error retained joined underlying causes")
	}
	if err.Error() != traceDeclined().Error() {
		t.Error("close error omitted the bounded store category and actionable next step")
	}
}

func TestTraceStoreClosePrivacy(t *testing.T) {
	t.Run("repeated_writer", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		w := storeWrite(t, c, "sentinel-lane.jsonl", []byte("owned bytes"))
		if err := w.Close(); err != nil {
			t.Fatal("first writer close failed")
		}
		if err := w.Close(); err != nil {
			assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
		}
	})
	t.Run("failed_writer_still_closes_claim", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		w := storeWrite(t, c, "sentinel-lane.jsonl", []byte("owned bytes"))
		if err := w.file.Close(); err != nil {
			t.Fatal("owned fault setup failed")
		}
		assertTraceStoreClosePrivacy(t, w.Close(), root, private, filepath.Dir(root))
		if _, err := w.claim.lock.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Error("writer close stopped before closing its claim lock")
		}
		if _, err := w.claim.dir.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Error("writer close stopped before closing its claim directory")
		}
	})
	t.Run("failed_claim_still_closes_directory", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		if err := c.lock.Close(); err != nil {
			t.Fatal("owned fault setup failed")
		}
		assertTraceStoreClosePrivacy(t, c.Close(), root, private, filepath.Dir(root))
		if _, err := c.dir.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Error("claim close stopped before closing its directory")
		}
		if err := c.Close(); err != nil {
			assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
		}
	})
	t.Run("root_handles_both_attempted", func(t *testing.T) {
		for _, first := range []string{"public", "private"} {
			t.Run(first, func(t *testing.T) {
				s, root, private := traceStoreFixture(t)
				fault := s.root
				if first == "private" {
					fault = s.private
				}
				if err := fault.Close(); err != nil {
					t.Fatal("owned root fault setup failed")
				}
				// os.Root.Close may already be idempotent. Any failure must still be safe.
				if err := s.Close(); err != nil {
					assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
				}
				for _, handle := range []*os.Root{s.root, s.private} {
					if _, err := handle.Stat("."); !errors.Is(err, os.ErrClosed) {
						t.Error("store close did not attempt both root handles")
					}
				}
			})
		}
	})
}

func TestTraceStoreFreshMint(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	storeWrite(t, c, "launcher-1.jsonl", []byte("visible now\n"))
	p, err := os.ReadFile(filepath.Join(root, c.ID(), "launcher-1.jsonl"))
	if err != nil || string(p) != "visible now\n" {
		t.Fatalf("immediate append: %q %v", p, err)
	}
	p, err = os.ReadFile(filepath.Join(root, c.ID(), "meta.json"))
	var meta map[string]any
	if err != nil || json.Unmarshal(p, &meta) != nil || len(meta) != 7 {
		t.Fatalf("seven metadata keys: %q %v", p, err)
	}
	if _, err := c.CreateWriter("launcher-1.jsonl"); err == nil {
		t.Fatal("second lane owner accepted")
	}
	if err := c.Diagnostic("capacity"); err != nil {
		t.Fatal(err)
	}
	if err := c.Diagnostic("again"); err == nil {
		t.Fatal("diagnostic reserve used twice")
	}
}

func TestTraceStoreCountAndStartIDTie(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	for n := 1; n <= 50; n++ {
		c := storeMint(t, s, n, 10)
		c.Close()
	}
	storeMint(t, s, 51, 9).Close()
	if _, err := os.Stat(filepath.Join(root, storeMeta(1, 0).Trace)); !os.IsNotExist(err) {
		t.Fatalf("oldest equal-start ID not removed: %v", err)
	}
	storeMint(t, s, 52, 20)
	if _, err := os.Stat(filepath.Join(root, storeMeta(51, 0).Trace)); !os.IsNotExist(err) {
		t.Fatalf("start ordering not used: %v", err)
	}
	st, err := s.Usage()
	if err != nil || st.Traces != 50 {
		t.Fatalf("count: %+v %v", st, err)
	}
}

func TestTraceStoreLiveAndUnknownLiveness(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	for n := 1; n <= 50; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Mint(storeMeta(51, 51)); err == nil {
		t.Fatal("live traces pruned")
	}
	if err := os.Remove(filepath.Join(root, storeMeta(1, 0).Trace, ".identity")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(52, 52)); err == nil {
		t.Fatal("unknown ownership/liveness accepted")
	}
}

func TestTraceStorePerTraceExactBoundary(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	u, err := c.Usage()
	if err != nil {
		t.Fatal(err)
	}
	w := storeWrite(t, c, "lane.jsonl", bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes-u.Bytes)))
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("trace over limit admitted")
	}
	u, err = c.Usage()
	if err != nil || u.Bytes != MaxTraceContentBytes {
		t.Fatalf("boundary: %+v %v", u, err)
	}
}

func TestTraceStoreGlobalExactBoundaryAndUnknownPrivate(t *testing.T) {
	s, _, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	u, err := s.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// Unknown private content remains charged machine-wide, never silently reclaimed.
	if err := os.WriteFile(filepath.Join(private, "orphan"), bytes.Repeat([]byte{'x'}, int(MaxStoreContentBytes-u.Bytes-1)), 0o600); err != nil {
		t.Fatal(err)
	}
	w := storeWrite(t, c, "lane.jsonl", []byte("x"))
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("machine cap exceeded")
	}
	u, err = s.Usage()
	if err != nil || u.Bytes != MaxStoreContentBytes {
		t.Fatalf("machine boundary: %+v %v", u, err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("mint at machine cap accepted")
	}
}

func TestTraceStorePrivateControlCrashLeftAndOverBudget(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	if err := c.ReplacePrivate("registry", []byte("locator")); err != nil {
		t.Fatal(err)
	}
	u, err := c.Usage()
	if err != nil {
		t.Fatal(err)
	}
	for path, n := range map[string]int{filepath.Join(root, c.ID(), ".crash-left"): 17, filepath.Join(private, c.ID(), "control"): 13} {
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := c.Usage()
	if err != nil || after.Bytes != u.Bytes+30 {
		t.Fatalf("private/control/crash accounting: %+v -> %+v %v", u, after, err)
	}
	if err := os.WriteFile(filepath.Join(root, "unknown-control"), bytes.Repeat([]byte{'x'}, int(MaxStoreContentBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("over-budget legacy store shrunk or admitted")
	}
	if _, err := os.Stat(filepath.Join(root, "unknown-control")); err != nil {
		t.Fatal("unknown content removed")
	}
}

func TestTraceStoreReplacementPeakAndFailedPublication(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	old := []byte("last good snapshot")
	if err := c.Replace("jail.jsonl", old); err != nil {
		t.Fatal(err)
	}
	u, _ := c.Usage()
	storeWrite(t, c, "lane.jsonl", bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes-u.Bytes)))
	if err := c.Replace("jail.jsonl", []byte("new")); err == nil {
		t.Fatal("replacement discounted old copy")
	}
	got, _ := os.ReadFile(filepath.Join(root, c.ID(), "jail.jsonl"))
	if !bytes.Equal(got, old) {
		t.Fatal("last good snapshot lost")
	}
	// A substituted nonregular publication target cannot be followed or removed.
	if err := os.Symlink("jail.jsonl", filepath.Join(root, c.ID(), "bad")); err != nil {
		t.Fatal(err)
	}
	if err := c.Replace("bad", []byte("x")); err == nil {
		t.Fatal("unsafe publication accepted")
	}
	got, _ = os.ReadFile(filepath.Join(root, c.ID(), "jail.jsonl"))
	if !bytes.Equal(got, old) {
		t.Fatal("failed publication changed old data")
	}
}

func TestTraceStoreReplacementCannotUnlinkLiveWriter(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	w := storeWrite(t, c, "lane.jsonl", []byte("owned lane"))
	if err := c.Replace("lane.jsonl", []byte("replacement")); err == nil {
		t.Fatal("replacement unlinked a live writer's physical content")
	}
	if n, err := w.Write([]byte("!")); err != nil || n != 1 {
		t.Fatalf("refused replacement broke writer: %d %v", n, err)
	}
	p, _ := os.ReadFile(filepath.Join(root, c.ID(), "lane.jsonl"))
	if string(p) != "owned lane!" {
		t.Fatalf("original content not retained: %q", p)
	}
}

func TestTraceStoreDiagnosticReserveAcrossClaims(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	if err := c.Diagnostic("capacity"); err != nil {
		t.Fatal(err)
	}
	child, err := s.Join(c.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err := child.Diagnostic("again"); err == nil {
		t.Fatal("second claim replaced admitted diagnostic evidence")
	}
}

func TestTraceStoreIncompatibleIdentityDeclinesBeforeCapacity(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	c.Close()
	if err := os.Remove(filepath.Join(root, c.ID(), ".identity")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("uncertain identity admitted new recording below capacity")
	}
	if _, err := os.Stat(filepath.Join(root, c.ID())); err != nil {
		t.Fatal("uncertain trace pruned")
	}
}

func TestTraceStoreMetadataRejectsPrivatePayloads(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	meta := storeMeta(1, 1)
	meta.Kind = "/private/path"
	if _, err := s.Mint(meta); err == nil {
		t.Fatal("path became public metadata")
	}
}

func TestTraceStoreOrphanPrivateTraceOverBudget(t *testing.T) {
	s, _, private := traceStoreFixture(t)
	id := storeMeta(99, 1).Trace
	if err := os.Mkdir(filepath.Join(private, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, id, "registry"), bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(1, 1)); err == nil {
		t.Fatal("orphan attributable trace already exceeds its cap")
	}
}

func TestTraceStoreFailedAppendDisablesLane(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	w := storeWrite(t, c, "lane.jsonl", []byte("last good"))
	// Fault only this owned fixture descriptor, then drive the production write entry.
	w.file.Close()
	if n, err := w.Write([]byte("broken")); err == nil || n != 0 {
		t.Fatalf("failed append reported stored: %d %v", n, err)
	}
	if n, err := w.Write([]byte("retry")); err == nil || n != 0 {
		t.Fatalf("disabled lane retried: %d %v", n, err)
	}
	p, _ := os.ReadFile(filepath.Join(root, c.ID(), "lane.jsonl"))
	if string(p) != "last good" {
		t.Fatalf("failed append lost admitted bytes: %q", p)
	}
}

func TestTraceStoreReplacedLockAndDirectoryIdentity(t *testing.T) {
	for _, object := range []string{"lock", "directory", "admission"} {
		t.Run(object, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			c := storeMint(t, s, 1, 1)
			path := filepath.Join(root, c.ID(), "lock")
			if object == "directory" {
				path = filepath.Join(root, c.ID())
			} else if object == "admission" {
				path = filepath.Join(root, ".admission")
			}
			if err := os.Rename(path, path+"-old"); err != nil {
				t.Fatal(err)
			}
			if object == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Join(c.ID()); err == nil {
				t.Fatal("replacement identity joined")
			}
			if _, err := c.CreateWriter("late.jsonl"); err == nil {
				t.Fatal("old claim wrote into replacement")
			}
		})
	}
}

func TestTraceStoreLateJoinAfterPrune(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	id := c.ID()
	c.Close()
	for n := 2; n <= 51; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Join(id); err == nil {
		t.Fatal("late join recreated trace")
	}
	if _, err := os.Stat(filepath.Join(root, id)); !os.IsNotExist(err) {
		t.Fatalf("pruned directory recreated: %v", err)
	}
}

func TestTraceStoreInabilityAccountAndContention(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	f, err := os.OpenFile(filepath.Join(root, ".admission"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateWriter("busy.jsonl"); err == nil {
		t.Fatal("contended admission did not decline")
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := os.Symlink(root, filepath.Join(private, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("unaccountable symlink accepted")
	}
}

// Re-exec helpers use this binary's TestMain isolation and inherited fixture TMPDIR.
func TestTraceStoreProcessHelper(t *testing.T) {
	mode := os.Getenv("TRACE_STORE_TEST_MODE")
	if mode == "" {
		t.Skip("owned re-exec helper only")
	}
	s, err := OpenTraceStore(os.Getenv("TRACE_STORE_TEST_ROOT"), os.Getenv("TRACE_STORE_TEST_PRIVATE"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	var c *TraceClaim
	if mode == "join" {
		c, err = s.Join(os.Getenv("TRACE_STORE_TEST_ID"))
	} else {
		var n int
		fmt.Sscan(os.Getenv("TRACE_STORE_TEST_N"), &n)
		c, err = s.Mint(storeMeta(n, int64(n)))
	}
	if err != nil {
		fmt.Println("declined")
		return
	}
	defer c.Close()
	fmt.Println("claimed")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
}

func storeChild(t *testing.T, root, private, mode, id string, n int) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTraceStoreProcessHelper$", "-test.timeout=45m")
	cmd.Env = testtmp.ChildEnv(append(os.Environ(), "TRACE_STORE_TEST_MODE="+mode, "TRACE_STORE_TEST_ROOT="+root, "TRACE_STORE_TEST_PRIVATE="+private, "TRACE_STORE_TEST_ID="+id, fmt.Sprintf("TRACE_STORE_TEST_N=%d", n)))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(out)
	if line, err := r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child ready %q: %v", line, err)
	}
	t.Cleanup(func() { in.Close(); cmd.Process.Kill(); cmd.Wait() })
	return cmd, in, r
}

func TestTraceStoreConcurrentMintersAcrossProcesses(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	for n := 1; n <= 49; n++ {
		storeMint(t, s, n, int64(n))
	}
	_, a, ar := storeChild(t, root, private, "mint", "", 50)
	_, b, br := storeChild(t, root, private, "mint", "", 51)
	fmt.Fprintln(a, "go")
	fmt.Fprintln(b, "go")
	la, ea := ar.ReadString('\n')
	lb, eb := br.ReadString('\n')
	if ea != nil || eb != nil || (strings.TrimSpace(la) != "claimed" && strings.TrimSpace(lb) != "claimed") {
		t.Fatalf("minters: %q %v / %q %v", la, ea, lb, eb)
	}
	if strings.TrimSpace(la) == "claimed" && strings.TrimSpace(lb) == "claimed" {
		t.Fatal("two processes exceeded capacity")
	}
	u, err := s.Usage()
	if err != nil || u.Traces != 50 {
		t.Fatalf("race occupancy: %+v %v", u, err)
	}
}

func TestTraceStoreContinuousChildHandover(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	parent := storeMint(t, s, 1, 1)
	_, in, r := storeChild(t, root, private, "join", parent.ID(), 0)
	fmt.Fprintln(in, "go")
	if line, err := r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "claimed" {
		t.Fatalf("child acquired claim: %q %v", line, err)
	}
	parent.Close() // Never unlock a shared file description inherited from the parent.
	for n := 2; n <= 50; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Mint(storeMeta(51, 51)); err == nil {
		t.Fatal("parent unlock released child's protection")
	}
}
