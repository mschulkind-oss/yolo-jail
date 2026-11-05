package resourcegroups

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ManagerQuery reads one explicitly named systemd unit's ControlGroup property.
// Callers invoke it only after resource-group selection succeeds.
type ManagerQuery func(argv []string) (string, error)

// ResolveSelectedIOPaths asks only the system and user managers for the selected unit,
// refuses ambiguous ownership, maps the reported cgroup-v2 path through mountinfo, and
// enumerates devices only from that group's io.max file. It performs no writes or scans.
func ResolveSelectedIOPaths(unit string, query ManagerQuery, mountInfo []byte, readFile IOReadFile) (IOPaths, error) {
	if unit == "" || query == nil || readFile == nil {
		return IOPaths{}, fmt.Errorf("selected resource-group observer is unavailable")
	}
	controlGroup, userManager, err := selectedControlGroup(unit, query)
	if err != nil {
		return IOPaths{}, err
	}
	mount, err := cgroup2MountFor(mountInfo, controlGroup)
	if err != nil {
		return IOPaths{}, err
	}
	assignedDir, err := mapControlGroup(mount, controlGroup)
	if err != nil {
		return IOPaths{}, err
	}
	assigned := filepath.Join(assignedDir, "io.max")
	data, err := readFile(assigned)
	if err != nil {
		return IOPaths{}, fmt.Errorf("selected unit %q io.max is unavailable: %w", unit, err)
	}
	parsed, _ := parseIOMax(data)
	devices := make([]string, 0, len(parsed.values)+len(parsed.ambiguous))
	for device := range parsed.values {
		devices = append(devices, device)
	}
	for device := range parsed.ambiguous {
		devices = append(devices, device)
	}
	sort.Strings(devices)
	devices = uniqueStrings(devices)
	if len(devices) == 0 {
		return IOPaths{}, fmt.Errorf("selected unit %q io.max has no device records; device policy is unknown (no host-wide device scan performed)", unit)
	}
	ancestors := make([]string, 0)
	for dir := filepath.Dir(assignedDir); dir != assignedDir; dir = filepath.Dir(dir) {
		ancestors = append(ancestors, filepath.Join(dir, "io.max"))
		if dir == mount.mountPoint {
			break
		}
	}
	return IOPaths{Devices: devices, Assigned: assigned, AssignedData: append([]byte(nil), data...), AssignedDataObserved: true, Ancestors: ancestors, UserManager: userManager}, nil
}

func selectedControlGroup(unit string, query ManagerQuery) (string, bool, error) {
	commands := [][]string{
		{"systemctl", "show", "--property=ControlGroup", "--value", "--", unit},
		{"systemctl", "--user", "show", "--property=ControlGroup", "--value", "--", unit},
	}
	var found []string
	foundUser := false
	systemManagerObserved := false
	for i, command := range commands {
		value, err := query(command)
		if err != nil {
			continue
		}
		if i == 0 {
			systemManagerObserved = true
		}
		value = strings.TrimSpace(value)
		if !validControlGroup(value) {
			continue
		}
		found = append(found, value)
		if i == 1 {
			foundUser = true
		}
	}
	switch len(found) {
	case 1:
		return found[0], foundUser && systemManagerObserved, nil
	case 0:
		return "", false, fmt.Errorf("selected unit %q has no verified ControlGroup in either systemd manager", unit)
	default:
		return "", false, fmt.Errorf("selected unit %q is present in both systemd managers; manager ownership is ambiguous", unit)
	}
}

func validControlGroup(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

type cgroupMount struct {
	root       string
	mountPoint string
}

func cgroup2MountFor(mountInfo []byte, controlGroup string) (cgroupMount, error) {
	var matches []cgroupMount
	for _, line := range strings.Split(string(mountInfo), "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		post := strings.Fields(after)
		if len(post) < 1 || post[0] != "cgroup2" {
			continue
		}
		pre := strings.Fields(before)
		if len(pre) < 5 {
			continue
		}
		root, err1 := unescapeMountInfo(pre[3])
		mountPoint, err2 := unescapeMountInfo(pre[4])
		if err1 != nil || err2 != nil || !filepath.IsAbs(root) || !filepath.IsAbs(mountPoint) || filepath.Clean(root) != root || filepath.Clean(mountPoint) != mountPoint {
			continue
		}
		if pathWithin(controlGroup, root) {
			matches = append(matches, cgroupMount{root: root, mountPoint: mountPoint})
		}
	}
	if len(matches) == 0 {
		return cgroupMount{}, fmt.Errorf("no cgroup-v2 mount maps selected unit path %q", controlGroup)
	}
	sort.Slice(matches, func(i, j int) bool { return len(matches[i].root) > len(matches[j].root) })
	if len(matches) > 1 && len(matches[0].root) == len(matches[1].root) {
		return cgroupMount{}, fmt.Errorf("multiple cgroup-v2 mounts map selected unit path %q", controlGroup)
	}
	return matches[0], nil
}

func mapControlGroup(mount cgroupMount, controlGroup string) (string, error) {
	if !pathWithin(controlGroup, mount.root) {
		return "", fmt.Errorf("selected unit path %q is outside cgroup mount root %q", controlGroup, mount.root)
	}
	relative := strings.TrimPrefix(controlGroup, mount.root)
	relative = strings.TrimPrefix(relative, string(filepath.Separator))
	mapped := filepath.Join(mount.mountPoint, relative)
	if !pathWithin(mapped, mount.mountPoint) || mapped == mount.mountPoint {
		return "", fmt.Errorf("selected unit path %q does not map to a child cgroup", controlGroup)
	}
	return mapped, nil
}

func pathWithin(path, root string) bool {
	if path == root {
		return true
	}
	if root == string(filepath.Separator) {
		return filepath.IsAbs(path)
	}
	return strings.HasPrefix(path, strings.TrimRight(root, string(filepath.Separator))+string(filepath.Separator))
}

func unescapeMountInfo(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("incomplete mountinfo escape")
		}
		code := value[i+1 : i+4]
		switch code {
		case "040":
			out.WriteByte(' ')
		case "011":
			out.WriteByte('\t')
		case "012":
			out.WriteByte('\n')
		case "134":
			out.WriteByte('\\')
		default:
			return "", fmt.Errorf("unsupported mountinfo escape \\%s", code)
		}
		i += 3
	}
	return out.String(), nil
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
