package cgd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestValidateCgroupNameGolden(t *testing.T) {
	cases := map[string]bool{
		"job": true, "training-1": true, "a.b_c-d": true, "": false,
		"-leading": false, "with/slash": false, "..": false, "a..b": false,
		"UPPER": true, "1digit": true, "has space": false,
	}
	for name, want := range cases {
		if got := ValidateCgroupName(name); got != want {
			t.Errorf("ValidateCgroupName(%q) = %v, want %v", name, got, want)
		}
	}
	// 64-char name valid, 65 invalid (the {0,63} bound after the first char).
	if !ValidateCgroupName(repeatByte('x', 64)) {
		t.Error("64-char name should be valid")
	}
	if ValidateCgroupName(repeatByte('x', 65)) {
		t.Error("65-char name should be invalid")
	}
}

func TestParseMemoryValueGolden(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"8g", 8589934592, true},
		{"512m", 536870912, true},
		{"1024k", 1048576, true},
		{"1048576", 1048576, true},
		{"0.5g", 536870912, true},
		{"2G", 2147483648, true},
		{"  4g  ", 4294967296, true},
		{"1.5m", 1572864, true},
		{"-1", -1, true},
		{"notanumber", 0, false},
		{"", 0, false},
		{"1x", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseMemoryValue(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParseMemoryValue(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestHandleCreateJoinDestroy exercises the real cgroup ops against a FAKE
// cgroup tree (plain temp dirs — the writes are just files here), proving the
// dispatch, path layout, and response shapes without root/real cgroups.
func TestHandleCreateJoinDestroy(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "container")
	// Seed the container cgroup with the control files ensureAgentCgroup reads.
	must(t, os.MkdirAll(container, 0o755))
	must(t, os.WriteFile(filepath.Join(container, "cgroup.controllers"), []byte("cpu memory pids"), 0o644))
	must(t, os.WriteFile(filepath.Join(container, "cgroup.procs"), []byte(""), 0o644))
	must(t, os.WriteFile(filepath.Join(container, "cgroup.subtree_control"), []byte(""), 0o644))

	// status before delegation.
	req, _ := ParseRequest([]byte(`{"op":"status"}`))
	resp := Handle(req, container, 1234)
	if v, _ := resp.Get("ok"); v != true {
		t.Fatalf("status ok=%v", v)
	}
	if v, _ := resp.Get("delegated"); v != false {
		t.Errorf("status delegated=%v, want false pre-create", v)
	}

	// create_and_join with a pid limit; use a bogus name -> rejected.
	req, _ = ParseRequest([]byte(`{"op":"create_and_join","name":"../evil","pids":10}`))
	resp = Handle(req, container, 4242)
	if v, _ := resp.Get("ok"); v != false {
		t.Errorf("traversal name should be rejected")
	}

	// A valid create_and_join writes the job cgroup files.
	req, _ = ParseRequest([]byte(`{"op":"create_and_join","name":"job1","pids":10}`))
	resp = Handle(req, container, 4242)
	if v, _ := resp.Get("ok"); v != true {
		t.Fatalf("create_and_join ok=%v resp=%v", v, dumpResp(resp))
	}
	jobCg := filepath.Join(container, "agent", "job1")
	if b, _ := os.ReadFile(filepath.Join(jobCg, "pids.max")); string(b) != "10" {
		t.Errorf("pids.max = %q, want 10", b)
	}
	if b, _ := os.ReadFile(filepath.Join(jobCg, "cgroup.procs")); string(b) != "4242" {
		t.Errorf("cgroup.procs = %q, want 4242 (caller moved in)", b)
	}

	// destroy: still has the pid we "moved in", so it must refuse.
	req, _ = ParseRequest([]byte(`{"op":"destroy","name":"job1"}`))
	resp = Handle(req, container, 4242)
	if v, _ := resp.Get("ok"); v != false {
		t.Errorf("destroy with procs should refuse, got ok=%v", v)
	}
	if v, _ := resp.Get("error"); v == nil {
		t.Error("destroy refusal must carry an error message")
	}

	// destroy of an ABSENT cgroup is idempotent-ok (Python: already gone).
	req, _ = ParseRequest([]byte(`{"op":"destroy","name":"never-existed"}`))
	resp = Handle(req, container, 4242)
	if v, _ := resp.Get("ok"); v != true {
		t.Errorf("destroy of absent cgroup should be idempotent-ok, got %v", dumpResp(resp))
	}

	// The rmdir-success path (empty cgroup.procs -> rmdir) is only faithfully
	// exercisable on a REAL cgroup v2 fs, where the interface files are
	// kernel-virtual and rmdir removes them atomically. On this regular-file
	// tmpdir tree those files block rmdir, so that path is covered by the
	// nested-jail verification, not this unit test.
}

// --- helpers ---

func repeatByte(b byte, n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = b
	}
	return string(s)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func dumpResp(m *jsonx.OrderedMap) string {
	s, _ := jsonx.DumpsCompact(m)
	return s
}
