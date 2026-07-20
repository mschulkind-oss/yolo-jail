package run

import (
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestKVMPlusROCmKeepGroupsOnce is the regression for the 2026-07-19 host
// failure: with AMD passthrough AND kvm both enabled on podman, the ROCm
// block and the kvm block each appended --group-add keep-groups, and podman
// hard-errors when keep-groups is combined with any other --group-add value —
// including a duplicate of itself ("the '--group-add keep-groups' option is
// not allowed with any other --group-add options"). The assemble wiring
// passes slices.Contains(runCmd, "keep-groups") into kvmArgs; this test pins
// the composed behavior at that seam.
func TestKVMPlusROCmKeepGroupsOnce(t *testing.T) {
	o := &Options{
		IsMacOS: false,
		PathExists: func(p string) bool {
			switch p {
			case "/dev/kvm", "/dev/kfd", "/dev/dri":
				return true
			}
			return false
		},
		Now:         func() time.Time { return time.Unix(0, 0) },
		IsTTYStdout: func() bool { return false },
		Stdout:      discardBuf(),
		Stderr:      discardBuf(),
	}
	gpuSec := jsonx.NewOrderedMap()
	gpuSec.Set("enabled", true)
	gpuSec.Set("vendor", "amd")
	cfg := newConfig("gpu", gpuSec, "kvm", true)

	// Mirror assemble.go's composition: gpu args first, then kvm with the
	// keep-groups-already-present fact.
	args := o.gpuArgs(cfg, "podman", true, "amd")
	args = append(args, o.kvmArgs(cfg, "podman", slices.Contains(args, "keep-groups"))...)

	if got := countOccurrences(args, "keep-groups"); got != 1 {
		t.Fatalf("keep-groups appears %d times, want exactly 1\nargs: %v", got, args)
	}
	if !slices.Contains(args, "/dev/kvm") {
		t.Fatalf("kvm device missing — kvm block did not run\nargs: %v", args)
	}
	if !slices.Contains(args, "/dev/kfd") {
		t.Fatalf("kfd device missing — gpu block did not run\nargs: %v", args)
	}

	// Without the ROCm block (kvm alone), kvm still adds keep-groups itself.
	solo := o.kvmArgs(cfg, "podman", false)
	if got := countOccurrences(solo, "keep-groups"); got != 1 {
		t.Fatalf("kvm-alone keep-groups appears %d times, want 1\nargs: %v", got, solo)
	}
}

func countOccurrences(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}
