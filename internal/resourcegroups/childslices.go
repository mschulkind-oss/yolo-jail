package resourcegroups

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ChildSeams are the injected reads and manager calls EnsureChildSlices makes.
// Query runs one full argv (it always begins "systemctl") and returns stdout; an
// error from it should carry the command's stderr.
type ChildSeams struct {
	Query     ManagerQuery
	MountInfo []byte
	ReadFile  IOReadFile
}

// ChildSlices are the two systemd child slices of a mapped slice, with the
// cgroup directories the user manager reported for them after they started.
type ChildSlices struct {
	Unit     string
	Agent    string
	Batch    string
	AgentDir string
	BatchDir string
}

// ChildUnits names the agent and batch children of a mapped slice. systemd's
// dash nesting makes "<stem>-agent.slice" a child of "<stem>.slice", so a launch
// never writes a cgroup file systemd owns (RG-R6). A unit that is not a slice has
// no such child, and the refusal says so.
func ChildUnits(unit string) (agent, batch string, err error) {
	stem, ok := strings.CutSuffix(unit, ".slice")
	if !ok {
		return "", "", fmt.Errorf("resource group %q is not a slice, and Yolo's two children are systemd child slices (%s-agent.slice, %s-batch.slice) that only a slice can contain; map a .slice unit, then run yolo check --no-build", unit, unit, unit)
	}
	if stem == "" || stem == "-" || strings.ContainsAny(stem, " \t\n/") || strings.HasPrefix(stem, "-") || strings.HasSuffix(stem, "-") {
		return "", "", fmt.Errorf("resource group %q cannot have child slices named after it; map a named slice such as batch-yolo.slice, then run yolo check --no-build", unit)
	}
	return stem + "-agent.slice", stem + "-batch.slice", nil
}

// ioProperty maps an io.max dimension to its systemd resource-control property.
var ioProperty = map[Dimension]string{
	ReadBytes:  "IOReadBandwidthMax",
	WriteBytes: "IOWriteBandwidthMax",
	ReadOps:    "IOReadIOPSMax",
	WriteOps:   "IOWriteIOPSMax",
}

// EnsureChildSlices creates (or reuses) the two child slices of the mapped
// slice through the user manager and proves the result. It sets each child's
// limits from the finite split of the mapped slice's allowance with
// `systemctl --user set-property --runtime`, starts both so systemd realizes
// their cgroups, then reads each child's own io.max back and refuses unless it
// equals the plan. It writes no cgroup file itself. Every refusal names the next
// step. The caller has already observed paths; this re-derives nothing from the
// unit name beyond the child names.
func EnsureChildSlices(ctx context.Context, unit string, paths IOPaths, percent int, s ChildSeams) (*ChildSlices, error) {
	if ctx == nil {
		return nil, errors.New("child-slice context missing; run yolo check --no-build")
	}
	if s.Query == nil || s.ReadFile == nil {
		return nil, errors.New("the user manager cannot be queried; run yolo check --no-build")
	}
	agent, batch, err := ChildUnits(unit)
	if err != nil {
		return nil, err
	}
	if err := CheckDelegatedIO(paths, s.ReadFile); err != nil {
		return nil, err
	}
	expected := map[string]map[string]map[Dimension]uint64{agent: {}, batch: {}}
	order := []string{} // devices in plan order
	props := map[string][]string{}
	for _, row := range ReadAndPlanIO(paths, percent, s.ReadFile).Dimensions {
		if row.State != StateFinite || row.SplitError != "" {
			continue // an unconstrained dimension gets no invented cap
		}
		if expected[agent][row.Device] == nil {
			expected[agent][row.Device] = map[Dimension]uint64{}
			expected[batch][row.Device] = map[Dimension]uint64{}
			order = append(order, row.Device)
		}
		expected[agent][row.Device][row.Dimension] = row.Agent
		expected[batch][row.Device][row.Dimension] = row.Batch
	}
	if len(order) == 0 {
		return nil, errors.New("assigned group has no finite I/O allowance to split: give the selected slice a finite IOReadBandwidthMax or IOWriteBandwidthMax, then run yolo check --no-build")
	}
	for _, child := range []string{agent, batch} {
		// Bandwidth before operations, reads before writes, devices in plan order.
		for _, dim := range []Dimension{ReadBytes, WriteBytes, ReadOps, WriteOps} {
			for _, device := range order {
				if value, ok := expected[child][device][dim]; ok {
					props[child] = append(props[child], fmt.Sprintf("%s=/dev/block/%s %d", ioProperty[dim], device, value))
				}
			}
		}
	}
	for _, child := range []string{agent, batch} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		argv := append([]string{"systemctl", "--user", "set-property", "--runtime", child}, props[child]...)
		if _, err := s.Query(argv); err != nil {
			return nil, fmt.Errorf("your user manager refused `systemctl --user set-property --runtime %s`: %w; check that `systemctl --user status` works in this terminal, then run yolo check --no-build", child, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.Query([]string{"systemctl", "--user", "start", agent, batch}); err != nil {
		return nil, fmt.Errorf("your user manager could not start %s and %s: %w; run `systemctl --user start %s %s` to see why, then run yolo check --no-build", agent, batch, err, agent, batch)
	}
	out := &ChildSlices{Unit: unit, Agent: agent, Batch: batch}
	parentDir := filepath.Dir(paths.Assigned)
	for _, child := range []string{agent, batch} {
		dir, err := readBackChild(child, parentDir, expected[child], s)
		if err != nil {
			return nil, err
		}
		if child == agent {
			out.AgentDir = dir
		} else {
			out.BatchDir = dir
		}
	}
	return out, ctx.Err()
}

// readBackChild asks the user manager where the child's cgroup is, requires it
// directly under the mapped slice's cgroup, and compares its io.max with the plan.
func readBackChild(child, parentDir string, want map[string]map[Dimension]uint64, s ChildSeams) (string, error) {
	got, err := ResolveSelectedIOPaths(child, s.Query, s.MountInfo, s.ReadFile)
	if err != nil {
		return "", fmt.Errorf("cannot read %s back after starting it: %w; its io.max should list the planned limits, so check `systemctl --user status %s`, then run yolo check --no-build", child, err, child)
	}
	dir := filepath.Dir(got.Assigned)
	if !got.UserManager || dir != filepath.Join(parentDir, child) {
		return "", fmt.Errorf("%s is at %s, not inside the mapped slice (%s); run yolo check --no-build", child, dir, parentDir)
	}
	observed, err := ParseIOMax(got.AssignedData)
	if err != nil {
		return "", fmt.Errorf("%s io.max is ambiguous: %w; run yolo check --no-build", child, err)
	}
	// In a fixed order (devices sorted, then rbps, wbps, riops, wiops), so a refusal always names
	// the same first mismatch rather than whichever one map iteration reached first.
	for _, device := range slices.Sorted(maps.Keys(want)) {
		dims := want[device]
		for _, dim := range dimensions {
			value, planned := dims[dim]
			if !planned {
				continue
			}
			limit := observed[device][dim]
			if !limit.Finite || limit.Value != value {
				return "", fmt.Errorf("%s io.max reads back %s %s=%s, but %d was set; systemd did not realize the limit, so run `systemctl --user status %s`, then yolo check --no-build", child, device, dim, formatLimit(limit), value, child)
			}
		}
	}
	for _, device := range slices.Sorted(maps.Keys(observed)) {
		dims := observed[device]
		for _, dim := range dimensions {
			limit, present := dims[dim]
			if _, planned := want[device][dim]; present && limit.Finite && !planned {
				return "", fmt.Errorf("%s holds a finite %s %s that the assigned policy no longer yields; run `systemctl --user revert %s`, then yolo check --no-build", child, device, dim, child)
			}
		}
	}
	return dir, nil
}

func formatLimit(l IOLimit) string {
	if !l.Finite {
		return "max"
	}
	return strconv.FormatUint(l.Value, 10)
}
