package resourcegroups

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReadAndPlanIOFiniteAssignedDimensions(t *testing.T) {
	root := t.TempDir()
	writeIOFile(t, root, "assigned/io.max", "8:0 rbps=101 wbps=20 riops=max wiops=1\n")
	plan := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "assigned/io.max")}, 5, os.ReadFile)
	got := plan.For("8:0", ReadBytes)
	if got.State != StateFinite || got.Agent != 5 || got.Batch != 96 || got.Allowance != 101 {
		t.Fatalf("rbps plan = %+v, want finite 101 split 5/96", got)
	}
	got = plan.For("8:0", WriteBytes)
	if got.State != StateFinite || got.Agent != 1 || got.Batch != 19 || got.Allowance != 20 {
		t.Fatalf("wbps plan = %+v, want finite 20 split 1/19", got)
	}
	if got := plan.For("8:0", ReadOps); got.State != StateMax || got.Agent != 0 || got.Batch != 0 {
		t.Fatalf("riops plan = %+v, want explicit max without a fabricated cap", got)
	}
	if got := plan.For("8:0", WriteOps); got.State != StateFinite || got.SplitError == "" {
		t.Fatalf("wiops plan = %+v, want finite-but-unrepresentable positive child split", got)
	}
}

func TestReadAndPlanIODistinguishesUnknownPolicyStates(t *testing.T) {
	root := t.TempDir()
	writeIOFile(t, root, "assigned/io.max", "8:0 rbps=max wbps=100 wiops=10\n8:1 rbps=500\n")
	writeIOFile(t, root, "ancestor/io.max", "8:0 rbps=0 riops=40 wiops=100\n")
	plan := ReadAndPlanIO(IOPaths{
		Devices: []string{"8:0", "8:2"}, Assigned: filepath.Join(root, "assigned/io.max"), Ancestors: []string{filepath.Join(root, "ancestor/io.max")},
	}, 5, os.ReadFile)
	checks := []struct {
		device string
		dim    Dimension
		state  PolicyState
	}{
		{"8:0", ReadBytes, StateMax},
		{"8:0", WriteBytes, StateFinite},
		{"8:0", ReadOps, StateAncestorOnly},
		{"8:0", WriteOps, StateFinite},
		{"8:2", ReadBytes, StateMissing}, // the observed 8:1 record must not be attributed to 8:2
	}
	for _, check := range checks {
		if got := plan.For(check.device, check.dim).State; got != check.state {
			t.Errorf("%s %s state = %q, want %q", check.device, check.dim, got, check.state)
		}
	}
	if got := plan.For("8:0", ReadBytes); got.State != StateMax || got.AncestorState != StateFinite || got.AncestorLimit != 0 {
		t.Fatalf("explicit max with zero-valued ancestor bound = %+v, want max plus finite zero ancestor", got)
	}
	incomplete := ReadAndPlanIO(IOPaths{
		Devices: []string{"8:0"}, Assigned: filepath.Join(root, "assigned/io.max"), Ancestors: []string{filepath.Join(root, "ancestor/io.max"), filepath.Join(root, "denied/io.max")},
	}, 5, func(path string) ([]byte, error) {
		if strings.Contains(path, "denied") {
			return nil, os.ErrPermission
		}
		return os.ReadFile(path)
	})
	if got := incomplete.For("8:0", ReadBytes); got.AncestorState != StateFinite || got.AncestorUncertainty != StateUnreadable || !strings.Contains(FormatIODimension(got), "known ancestor bound 0") || !strings.Contains(FormatIODimension(got), "some ancestor policy unreadable") {
		t.Fatalf("incomplete ancestor observation = %+v (%s), want known bound plus unreadable ancestry", got, FormatIODimension(got))
	}
	missing := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "absent/io.max")}, 5, os.ReadFile)
	if got := missing.For("8:0", ReadBytes); got.State != StateUnavailable || strings.Contains(FormatIODimension(got), "missing/unconstrained") {
		t.Fatalf("missing assigned policy state = %q (%s), want unavailable proof", got.State, FormatIODimension(got))
	}
	writeIOFile(t, root, "assigned-with-absent-dimension/io.max", "8:0 rbps=10\n")
	absentDimension := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "assigned-with-absent-dimension/io.max")}, 5, os.ReadFile)
	if got := absentDimension.For("8:0", WriteBytes); got.State != StateMissing || !strings.Contains(FormatIODimension(got), "missing/unconstrained") {
		t.Fatalf("successfully read absent dimension = %q (%s), want missing/unconstrained", got.State, FormatIODimension(got))
	}
	unreadable := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "denied/io.max")}, 5, func(string) ([]byte, error) { return nil, os.ErrPermission })
	if got := unreadable.For("8:0", ReadBytes).State; got != StateUnreadable {
		t.Fatalf("unreadable assigned policy state = %q, want unreadable", got)
	}
	writeIOFile(t, root, "duplicate/io.max", "8:0 rbps=1\n8:0 wbps=2\n")
	ambiguous := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "duplicate/io.max")}, 5, os.ReadFile)
	if got := ambiguous.For("8:0", ReadBytes).State; got != StateAmbiguous {
		t.Fatalf("duplicate-device policy state = %q, want ambiguous", got)
	}
}

func TestReadAndPlanIORejectsInvalidAndOverflowingSplitInputs(t *testing.T) {
	root := t.TempDir()
	writeIOFile(t, root, "max/io.max", "8:0 rbps="+strings.TrimSpace(strings.Repeat("9", 20))+"\n")
	plan := ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "max/io.max")}, 5, os.ReadFile)
	if got := plan.For("8:0", ReadBytes).State; got != StateAmbiguous {
		t.Fatalf("out-of-range finite value state = %q, want ambiguous", got)
	}
	writeIOFile(t, root, "uintmax/io.max", "8:0 rbps="+strconv.FormatUint(math.MaxUint64, 10)+"\n")
	plan = ReadAndPlanIO(IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "uintmax/io.max")}, 5, os.ReadFile)
	got := plan.For("8:0", ReadBytes)
	if got.State != StateFinite || got.Agent == 0 || got.Batch == 0 || got.Agent+got.Batch != math.MaxUint64 {
		t.Fatalf("maximum uint64 split = %+v, want positive exact sum without overflow", got)
	}
	if split, err := SplitAllowance(1, 5); err == nil || split.Agent != 0 {
		t.Fatalf("one-unit allowance split = %+v, %v, want actionable positive-child refusal", split, err)
	}
	for _, percent := range []int{1, 99} {
		split, err := SplitAllowance(100, percent)
		if err != nil || split.Agent != uint64(percent) || split.Batch != uint64(100-percent) {
			t.Errorf("100-unit split at %d%% = %+v, %v", percent, split, err)
		}
	}
	if _, err := SplitAllowance(100, 0); err == nil {
		t.Fatal("zero-percent split was accepted")
	}
}

func TestParseIOMaxRejectsDuplicateDeviceAndDuplicateDimension(t *testing.T) {
	for _, input := range []string{
		"8:0 rbps=1\n8:0 wbps=2\n",
		"8:0 rbps=1 rbps=2\n",
	} {
		if _, err := ParseIOMax([]byte(input)); err == nil || !errors.Is(err, ErrAmbiguousPolicy) {
			t.Errorf("ParseIOMax(%q) error = %v, want ambiguous policy", input, err)
		}
	}
}

func writeIOFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
