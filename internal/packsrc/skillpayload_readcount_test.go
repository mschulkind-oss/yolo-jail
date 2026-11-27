package packsrc

// N7 of docs/plans/git-skills-aws-verification.md: how many times does each production
// path read a whole skill payload, and how many payload bytes does it retain?
//
// The count is OS-level and needs no seam in skillpayload.go: /proc/self/io's rchar is every
// byte this process read(2)s, which includes the payload bytes that arrive from `git
// cat-file --batch` through a pipe and every payload file the tree readers open. Dividing
// that by the payload's size gives whole-payload reads. No timing is involved.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

const measureFiles = 16

// measureBytes is the payload size. The default keeps the short suite fast; the recorded N7
// numbers use YOLO_N7_PAYLOAD_BYTES near skillByteLimit.
func measureBytes(t *testing.T) int64 {
	t.Helper()
	if v := os.Getenv("YOLO_N7_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > skillByteLimit {
			t.Fatalf("YOLO_N7_PAYLOAD_BYTES=%q: want 1..%d", v, skillByteLimit)
		}
		return n
	}
	return 16 << 20
}

func procIOReadBytes(t *testing.T) int64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/io")
	if err != nil {
		t.Skipf("no /proc/self/io: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "rchar: "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Skip("no rchar in /proc/self/io")
	return 0
}

type readCost struct {
	readBytes, allocBytes int64
	// peakRSS is the process's resident high-water mark during fn, in bytes, or 0 when
	// the kernel will not let the test reset it (/proc/self/clear_refs). It includes the
	// test's own earlier heap, so only its growth over an idle baseline means anything.
	peakRSS int64
}

func resetPeakRSS() bool {
	return os.WriteFile("/proc/self/clear_refs", []byte("5"), 0) == nil
}

func peakRSS() int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			f := strings.Fields(v)
			if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
				return n << 10
			}
		}
	}
	return 0
}

// measureRead runs fn once and reports the bytes the process read and allocated.
func measureRead(t *testing.T, fn func()) readCost {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	reset := resetPeakRSS()
	r0 := procIOReadBytes(t)
	fn()
	r1 := procIOReadBytes(t)
	runtime.ReadMemStats(&after)
	cost := readCost{readBytes: r1 - r0, allocBytes: int64(after.TotalAlloc - before.TotalAlloc)}
	if reset {
		cost.peakRSS = peakRSS()
	}
	return cost
}

// retainedBytes is the payload bytes a plan keeps alive: its entries' Data, plus the
// marker/license copies on its rows.
func retainedBytes(p *SkillPayloadPlan) int64 {
	var n int64
	for _, e := range p.Entries {
		n += int64(len(e.Data))
	}
	for _, r := range p.Rows {
		n += int64(len(r.Marker)) + int64(len(r.License))
	}
	return n
}

// wholeReads converts a measured byte count to whole-payload reads, requiring it to sit
// within a small overhead of an integral number of payloads.
func wholeReads(t *testing.T, name string, cost readCost, payload int64) int {
	t.Helper()
	slack := payload/20 + (2 << 20) // marker, listing, lock and stamp reads
	n := int(cost.readBytes / payload)
	if cost.readBytes-int64(n)*payload > slack {
		t.Fatalf("%s: read %d bytes is not a whole number of %d-byte payloads (+%d slack)", name, cost.readBytes, payload, slack)
	}
	return n
}

func TestSkillPayloadWholePayloadReadCounts(t *testing.T) {
	payload := measureBytes(t)
	remote := gitRepo(t, map[string]string{"initial": "x"})
	gitIn(t, remote, "checkout", "-b", "skills-main")
	each := int(payload / measureFiles)
	for i := 0; i < measureFiles; i++ {
		// Distinct bytes per file so Git cannot dedupe the blobs.
		blob := bytes.Repeat([]byte(fmt.Sprintf("file-%02d\n", i)), each/8+1)[:each]
		writeSkillFixture(t, remote, fmt.Sprintf("skills/chosen/data/%02d.bin", i), string(blob), 0644)
	}
	writeSkillFixture(t, remote, "skills/chosen/SKILL.md", "---\nname: chosen\n---\nmeasured\n", 0644)
	gitIn(t, remote, "add", "--all")
	gitIn(t, remote, "commit", "-m", "payload")
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	testsupport.UnsealOnCleanup(t, s.Dir)
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	opts := SkillResolveOptions{LockPath: lock, Now: func() time.Time { return time.Unix(1800000000, 0) }}
	rows := []SkillRequest{{Label: "measure/skills[0]", Source: "git+file://" + remote + "//skills/chosen?ref=skills-main"}}

	var cold []SkillPayloadResult
	coldCost := measureRead(t, func() { cold = s.ResolveSkillPayloads(rows, opts) })
	if len(cold) != 1 || cold[0].State != SkillReady {
		t.Fatalf("cold resolve: %+v", cold)
	}
	size := retainedBytes(cold[0].Plan)
	if size < payload {
		t.Fatalf("fixture smaller than asked: %d < %d", size, payload)
	}
	got := map[string]int{}
	got["fresh launch, payload not yet published (ResolveSkillPayloads)"] = wholeReads(t, "cold resolve", coldCost, size)

	var warm []SkillPayloadResult
	warmCost := measureRead(t, func() { warm = s.ResolveSkillPayloads(rows, opts) })
	if warm[0].State != SkillReady {
		t.Fatalf("warm resolve: %+v", warm)
	}
	got["fresh launch, payload already published (ResolveSkillPayloads)"] = wholeReads(t, "warm resolve", warmCost, size)

	var observed []SkillPayloadResult
	observeCost := measureRead(t, func() { observed = s.ReadSkillPayloads(rows, lock) })
	if observed[0].State != SkillReady {
		t.Fatalf("observe: %+v", observed)
	}
	got["host observe (ReadSkillPayloads)"] = wholeReads(t, "observe", observeCost, size)

	// Attach, and every host reader of a staged tree, re-reads the tree once per row.
	var plan *SkillPayloadPlan
	attachCost := measureRead(t, func() {
		var err error
		plan, err = ReadSkillPayloadTreeBeneath(s.Dir, cold[0].Payload, rows)
		if err != nil {
			t.Fatal(err)
		}
	})
	got["attach, one row (ReadSkillPayloadTreeBeneath)"] = wholeReads(t, "attach", attachCost, size)
	if retainedBytes(plan) != size {
		t.Fatalf("retained %d, want %d", retainedBytes(plan), size)
	}

	// The launch stages the payload into its pack tree: one tree read, then the publish.
	dst := filepath.Join(t.TempDir(), "staged")
	copyCost := measureRead(t, func() {
		if err := CopySkillPayload(cold[0].Payload, dst); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { _ = RemoveSealedSkillTree(filepath.Dir(dst), dst) })
	got["launch staging copy (CopySkillPayload)"] = wholeReads(t, "copy", copyCost, size)

	want := map[string]int{
		"fresh launch, payload not yet published (ResolveSkillPayloads)": 2,
		"fresh launch, payload already published (ResolveSkillPayloads)": 2,
		"host observe (ReadSkillPayloads)":                               2,
		"attach, one row (ReadSkillPayloadTreeBeneath)":                  1,
		"launch staging copy (CopySkillPayload)":                         2,
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s: %d whole-payload reads, baseline %d", name, got[name], n)
		}
	}
	t.Logf("payload %d bytes (%d files); retained by one plan: %d bytes", size, measureFiles, retainedBytes(plan))
	for name, c := range map[string]readCost{"cold resolve": coldCost, "warm resolve": warmCost, "observe": observeCost, "attach": attachCost, "copy": copyCost} {
		t.Logf("N7 %-13s read=%d (%.2fx) allocated=%d (%.2fx) peakRSS=%d (%.2fx)", name, c.readBytes, float64(c.readBytes)/float64(size), c.allocBytes, float64(c.allocBytes)/float64(size), c.peakRSS, float64(c.peakRSS)/float64(size))
	}
	runtime.KeepAlive(plan)
}
