// Package resourcegroups observes selected manager groups and cgroup v2 I/O policy,
// and plans finite child allocations. Its uninvoked Linux-only adapter can reconcile
// delegated children; observation or planning alone does not prove enforcement.
package resourcegroups

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

var ErrAmbiguousPolicy = errors.New("ambiguous cgroup io.max policy")

// Dimension is one independently controlled cgroup v2 io.max dimension.
type Dimension string

const (
	ReadBytes  Dimension = "rbps"
	WriteBytes Dimension = "wbps"
	ReadOps    Dimension = "riops"
	WriteOps   Dimension = "wiops"
)

var dimensions = []Dimension{ReadBytes, WriteBytes, ReadOps, WriteOps}

// PolicyState preserves why a finite assigned allowance could not be derived.
type PolicyState string

const (
	StateFinite       PolicyState = "finite"
	StateMax          PolicyState = "max"
	StateMissing      PolicyState = "missing"
	StateUnavailable  PolicyState = "unavailable"
	StateUnreadable   PolicyState = "unreadable"
	StateAncestorOnly PolicyState = "ancestor-only"
	StateAmbiguous    PolicyState = "ambiguous"
)

// IOLimit is one assigned cgroup v2 dimension value; Finite=false represents max.
type IOLimit struct {
	Finite bool
	Value  uint64
}

type parsedIO struct {
	values    map[string]map[Dimension]IOLimit
	ambiguous map[string]bool
}

// ParseIOMax parses the kernel's cgroup v2 io.max records. Duplicate devices,
// duplicate dimensions, malformed values, and unknown fields are ambiguous rather
// than being resolved by record order.
func ParseIOMax(data []byte) (map[string]map[Dimension]IOLimit, error) {
	parsed, err := parseIOMax(data)
	if err != nil {
		return nil, err
	}
	if len(parsed.ambiguous) != 0 {
		return nil, ErrAmbiguousPolicy
	}
	return parsed.values, nil
}

func parseIOMax(data []byte) (parsedIO, error) {
	out := parsedIO{values: map[string]map[Dimension]IOLimit{}, ambiguous: map[string]bool{}}
	for lineNo, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		device := fields[0]
		if !validDeviceID(device) || len(fields) < 2 {
			return out, fmt.Errorf("%w: malformed io.max line %d", ErrAmbiguousPolicy, lineNo+1)
		}
		if _, exists := out.values[device]; exists || out.ambiguous[device] {
			out.ambiguous[device] = true
			delete(out.values, device)
			continue
		}
		limits := map[Dimension]IOLimit{}
		for _, field := range fields[1:] {
			key, raw, ok := strings.Cut(field, "=")
			if !ok {
				return out, fmt.Errorf("%w: malformed io.max field on line %d", ErrAmbiguousPolicy, lineNo+1)
			}
			dim := Dimension(key)
			if !knownDimension(dim) {
				return out, fmt.Errorf("%w: unknown io.max dimension %q", ErrAmbiguousPolicy, key)
			}
			if _, exists := limits[dim]; exists {
				out.ambiguous[device] = true
				continue
			}
			if raw == "max" {
				limits[dim] = IOLimit{}
				continue
			}
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return out, fmt.Errorf("%w: invalid %s value on line %d", ErrAmbiguousPolicy, dim, lineNo+1)
			}
			limits[dim] = IOLimit{Finite: true, Value: value}
		}
		out.values[device] = limits
	}
	return out, nil
}

func validDeviceID(value string) bool {
	major, minor, ok := strings.Cut(value, ":")
	if !ok || major == "" || minor == "" || strings.Contains(minor, ":") {
		return false
	}
	for _, part := range []string{major, minor} {
		parsed, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(parsed, 10) != part {
			return false
		}
	}
	return true
}

func knownDimension(dimension Dimension) bool {
	for _, known := range dimensions {
		if dimension == known {
			return true
		}
	}
	return false
}

// Split is an exact finite allocation. Agent is floor(allowance*agentPercent/100);
// Batch receives the remainder so their sum is exactly the assigned allowance.
type Split struct {
	Agent uint64
	Batch uint64
}

// SplitAllowance divides a positive finite assigned allowance without multiplying
// the full allowance by the percentage, which would overflow for large values.
func SplitAllowance(allowance uint64, agentPercent int) (Split, error) {
	if agentPercent < 1 || agentPercent > 99 {
		return Split{}, fmt.Errorf("agent percent must be from 1 through 99")
	}
	if allowance == 0 {
		return Split{}, fmt.Errorf("finite assigned allowance must be positive")
	}
	whole, remainder := allowance/100, allowance%100
	agent := whole*uint64(agentPercent) + remainder*uint64(agentPercent)/100
	batch := allowance - agent
	if agent == 0 || batch == 0 {
		return Split{Agent: agent, Batch: batch}, fmt.Errorf("allowance %d cannot produce positive finite children at %d%% agent share", allowance, agentPercent)
	}
	return Split{Agent: agent, Batch: batch}, nil
}

// IOPaths are obtained only after a real consumer has selected an owner mapping.
// Assigned and Ancestors name io.max files from the selected cgroup v2 group and
// its known ancestors; Devices is the exact device identity set under inspection.
// AssignedDataObserved keeps device enumeration and planning on the same read-only snapshot.
type IOPaths struct {
	Devices              []string
	Assigned             string
	AssignedData         []byte
	AssignedDataObserved bool
	Ancestors            []string
	// UserManager records the manager that resolved the selected unit, not a name-based guess.
	UserManager bool
}

type IOReadFile func(string) ([]byte, error)

type IODimensionPlan struct {
	Device              string
	Dimension           Dimension
	State               PolicyState
	Allowance           uint64
	AncestorState       PolicyState
	AncestorUncertainty PolicyState
	AncestorLimit       uint64
	Agent               uint64
	Batch               uint64
	SplitError          string
}

type IOPlan struct {
	AgentPercent int
	Dimensions   []IODimensionPlan
}

// For returns the plan row for an exact device and dimension pair.
func (plan IOPlan) For(device string, dimension Dimension) IODimensionPlan {
	for _, row := range plan.Dimensions {
		if row.Device == device && row.Dimension == dimension {
			return row
		}
	}
	return IODimensionPlan{Device: device, Dimension: dimension, State: StateMissing}
}

// ReadAndPlanIO reads only explicitly supplied assigned/ancestor io.max files and
// plans child ceilings for finite assigned entries. It never scans devices or writes policy.
func ReadAndPlanIO(paths IOPaths, agentPercent int, readFile IOReadFile) IOPlan {
	plan := IOPlan{AgentPercent: agentPercent}
	devices, duplicateDevices := normalizeDevices(paths.Devices)
	var assigned parsedIO
	var assignedErr error
	if paths.AssignedDataObserved {
		assigned, assignedErr = parseIOMax(paths.AssignedData)
	} else {
		assigned, assignedErr = readIOMax(paths.Assigned, readFile)
	}
	ancestors := make([]parsedIO, 0, len(paths.Ancestors))
	ancestorErrors := make([]error, 0, len(paths.Ancestors))
	for _, path := range paths.Ancestors {
		parsed, err := readIOMax(path, readFile)
		ancestors = append(ancestors, parsed)
		ancestorErrors = append(ancestorErrors, err)
	}
	for _, device := range devices {
		for _, dimension := range dimensions {
			row := IODimensionPlan{Device: device, Dimension: dimension, State: StateMissing, AncestorState: StateMissing}
			if duplicateDevices[device] {
				row.State = StateAmbiguous
				plan.Dimensions = append(plan.Dimensions, row)
				continue
			}
			var ancestorFinite bool
			ancestorLimitSeen := false
			var ancestorUnknown PolicyState
			for i, ancestor := range ancestors {
				if ancestorErrors[i] != nil {
					if ancestorUnknown == "" {
						ancestorUnknown = stateFromError(ancestorErrors[i])
					}
					continue
				}
				if ancestor.ambiguous[device] {
					ancestorUnknown = StateAmbiguous
					continue
				}
				if value, ok := ancestor.values[device][dimension]; ok && value.Finite {
					ancestorFinite = true
					if !ancestorLimitSeen || value.Value < row.AncestorLimit {
						row.AncestorLimit = value.Value
						ancestorLimitSeen = true
					}
				}
			}
			if ancestorFinite {
				row.AncestorState = StateFinite
				row.AncestorUncertainty = ancestorUnknown
			} else if ancestorUnknown != "" {
				row.AncestorState = ancestorUnknown
			}
			if assignedErr != nil {
				row.State = stateFromError(assignedErr)
				plan.Dimensions = append(plan.Dimensions, row)
				continue
			}
			if assigned.ambiguous[device] {
				row.State = StateAmbiguous
				plan.Dimensions = append(plan.Dimensions, row)
				continue
			}
			if limits, ok := assigned.values[device]; ok {
				if value, found := limits[dimension]; found {
					if value.Finite {
						row.State = StateFinite
						row.Allowance = value.Value
						split, err := SplitAllowance(value.Value, agentPercent)
						row.Agent, row.Batch = split.Agent, split.Batch
						if err != nil {
							row.SplitError = err.Error()
						}
					} else {
						row.State = StateMax
					}
				} else if ancestorFinite {
					row.State = StateAncestorOnly
				}
			} else if ancestorFinite {
				row.State = StateAncestorOnly
			}
			plan.Dimensions = append(plan.Dimensions, row)
		}
	}
	return plan
}

func normalizeDevices(input []string) ([]string, map[string]bool) {
	seen := map[string]bool{}
	duplicate := map[string]bool{}
	for _, device := range input {
		if !validDeviceID(device) || seen[device] {
			duplicate[device] = true
		}
		seen[device] = true
	}
	devices := make([]string, 0, len(seen))
	for device := range seen {
		devices = append(devices, device)
	}
	sort.Strings(devices)
	return devices, duplicate
}

func readIOMax(path string, readFile IOReadFile) (parsedIO, error) {
	if path == "" || readFile == nil {
		return parsedIO{}, os.ErrNotExist
	}
	data, err := readFile(path)
	if err != nil {
		return parsedIO{}, err
	}
	parsed, err := parseIOMax(data)
	if err != nil {
		return parsed, err
	}
	return parsed, nil
}

func stateFromError(err error) PolicyState {
	if errors.Is(err, ErrAmbiguousPolicy) {
		return StateAmbiguous
	}
	if errors.Is(err, os.ErrNotExist) {
		return StateUnavailable
	}
	return StateUnreadable
}

// FormatIODimension renders one row without implying that its proposed ceilings
// are active; callers must report placement and enforcement separately.
func FormatIODimension(row IODimensionPlan) string {
	prefix := fmt.Sprintf("%s %s: ", row.Device, row.Dimension)
	var rendered string
	switch row.State {
	case StateFinite:
		if row.SplitError != "" {
			rendered = prefix + fmt.Sprintf("finite assigned %d; cannot split: %s", row.Allowance, row.SplitError)
		} else {
			rendered = prefix + fmt.Sprintf("finite assigned %d agent=%d batch=%d", row.Allowance, row.Agent, row.Batch)
		}
	case StateMax:
		rendered = prefix + "assigned max (no finite allowance)"
	case StateAncestorOnly:
		rendered = prefix + fmt.Sprintf("ancestor-only finite bound %d (not an assigned allowance)", row.AncestorLimit)
	case StateUnreadable:
		rendered = prefix + "unreadable"
	case StateUnavailable:
		rendered = prefix + "unknown/unavailable (policy file could not be read)"
	case StateAmbiguous:
		rendered = prefix + "ambiguous"
	default:
		rendered = prefix + "missing/unconstrained"
	}
	if row.AncestorUncertainty != "" {
		rendered += "; some ancestor policy " + string(row.AncestorUncertainty)
	}
	if row.State != StateAncestorOnly {
		switch row.AncestorState {
		case StateFinite:
			rendered += fmt.Sprintf("; known ancestor bound %d", row.AncestorLimit)
		case StateUnavailable:
			rendered += "; ancestor policy unavailable"
		case StateUnreadable:
			rendered += "; ancestor policy unreadable"
		case StateAmbiguous:
			rendered += "; ancestor policy ambiguous"
		}
	}
	return rendered
}
