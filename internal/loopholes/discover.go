package loopholes

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// yolo-jail.jsonc loopholes: entries as Loophole records (transport
// unix-socket, lifecycle spawned, source config).
func synthesizeConfigLoopholes(loopholesConfig *jsonx.OrderedMap) []*Loophole {
	out := []*Loophole{}
	if loopholesConfig == nil {
		return out
	}
	for _, name := range loopholesConfig.Keys() {
		specV, _ := loopholesConfig.Get(name)
		spec, ok := specV.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		description := ""
		if dv, ok := spec.Get("description"); ok && pyTruthy(dv) {
			description = pyStr(dv)
		}
		enabled := true
		if ev, ok := spec.Get("enabled"); ok {
			enabled = pyTruthy(ev)
		}
		doctorCmd, doctorSet := []string(nil), false
		if dcv, ok := spec.Get("doctor_cmd"); ok {
			if list, isList := dcv.([]any); isList && allStrings(list) {
				doctorCmd = toStringSlice(list)
				doctorSet = true
			}
		}
		out = append(out, &Loophole{
			Name:         name,
			Description:  description,
			Path:         "<yolo-jail.jsonc:loopholes." + name + ">",
			Enabled:      enabled,
			Transport:    "unix-socket",
			Lifecycle:    "spawned",
			Intercepts:   []Intercept{},
			BrokerIP:     DefaultBrokerIP,
			JailEnv:      NewEnvMap(),
			DoctorCmd:    doctorCmd,
			DoctorCmdSet: doctorSet,
			Source:       SourceConfig,
		})
	}
	return out
}

// matching entries of `existing` in place and returns the NEW inline loopholes
// (in document order) that matched nothing.
func applyWorkspaceOverrides(existing map[string]*Loophole, loopholesConfig *jsonx.OrderedMap) []*Loophole {
	newInline := []*Loophole{}
	if loopholesConfig == nil {
		return newInline
	}
	for _, name := range loopholesConfig.Keys() {
		specV, _ := loopholesConfig.Get(name)
		spec, ok := specV.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		target := existing[name]
		if target == nil {
			single := jsonx.NewOrderedMap()
			single.Set(name, spec)
			newInline = append(newInline, synthesizeConfigLoopholes(single)...)
			continue
		}
		if enabledV, ok := spec.Get("enabled"); ok {
			target.Enabled = pyTruthy(enabledV)
		}
		if envV, ok := spec.Get("env"); ok && pyTruthy(envV) {
			if envMap, isMap := envV.(*jsonx.OrderedMap); isMap && target.HostDaemon != nil {
				override := NewEnvMap()
				for _, k := range envMap.Keys() {
					v, _ := envMap.Get(k)
					override.Set(k, pyStr(v))
				}
				target.HostDaemon.Env = target.HostDaemon.Env.MergedWith(override)
			}
		}
		if jailEnvV, ok := spec.Get("jail_env"); ok && pyTruthy(jailEnvV) {
			if jailEnvMap, isMap := jailEnvV.(*jsonx.OrderedMap); isMap {
				override := NewEnvMap()
				for _, k := range jailEnvMap.Keys() {
					v, _ := jailEnvMap.Get(k)
					override.Set(k, pyStr(v))
				}
				target.JailEnv = target.JailEnv.MergedWith(override)
			}
		}
	}
	return newInline
}

// hidden/non-dir children and malformed manifests silently. Returns an
// insertion-ordered slice of names alongside the map so callers can preserve
// Python dict order (sorted directory iteration).
func loadFromDir(dirPath, source string) (map[string]*Loophole, []string) {
	out := map[string]*Loophole{}
	var order []string
	fi, err := os.Stat(dirPath)
	if err != nil || !fi.IsDir() {
		return out, order
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return out, order
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, childName := range names {
		child := filepath.Join(dirPath, childName)
		cfi, err := os.Stat(child)
		if err != nil || !cfi.IsDir() || strings.HasPrefix(childName, ".") {
			continue
		}
		loophole, err := loadManifest(child)
		if err != nil {
			continue
		}
		loophole.Source = source
		if _, seen := out[loophole.Name]; !seen {
			order = append(order, loophole.Name)
		}
		out[loophole.Name] = loophole
	}
	return out, order
}

// DiscoverOptions carries the keyword arguments of discover_loopholes. The zero
// value corresponds to the Python defaults with IncludeBundled defaulting true
// via the Discover entry point.
type DiscoverOptions struct {
	Root            string // "" => UserLoopholesDir()
	RootSet         bool
	IncludeDisabled bool
	LoopholesConfig *jsonx.OrderedMap
	IncludeBundled  bool
}

// (include_bundled=True) should set IncludeBundled=true.
func Discover(opts DiscoverOptions) []*Loophole {
	root := opts.Root
	if !opts.RootSet || root == "" {
		root = UserLoopholesDir()
	}

	byName := map[string]*Loophole{}
	var order []string
	appendOrdered := func(m map[string]*Loophole, keys []string) {
		for _, k := range keys {
			if _, seen := byName[k]; !seen {
				order = append(order, k)
			}
			byName[k] = m[k]
		}
	}
	if opts.IncludeBundled {
		bm, bk := loadFromDir(BundledLoopholesDir(), SourceBundled)
		appendOrdered(bm, bk)
	}
	um, uk := loadFromDir(root, SourceUser)
	appendOrdered(um, uk)

	inline := applyWorkspaceOverrides(byName, opts.LoopholesConfig)

	out := []*Loophole{}
	for _, name := range order {
		m := byName[name]
		if !opts.IncludeDisabled && !m.Enabled {
			continue
		}
		out = append(out, m)
	}
	for _, m := range inline {
		if !opts.IncludeDisabled && !m.Enabled {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ValidateEntry is one result of ValidateLoopholes: the child dir, the loaded
// loophole (nil on error), and the error string ("" when OK).
type ValidateEntry struct {
	Path     string
	Loophole *Loophole
	Err      string
}

// loophole dir (bundled + user), reporting parse errors instead of skipping.
func ValidateLoopholes(root string, rootSet, includeBundled bool) []ValidateEntry {
	out := []ValidateEntry{}
	type dirSource struct {
		dir    string
		source string
	}
	var dirs []dirSource
	if includeBundled {
		dirs = append(dirs, dirSource{BundledLoopholesDir(), SourceBundled})
	}
	userRoot := root
	if !rootSet || userRoot == "" {
		userRoot = UserLoopholesDir()
	}
	dirs = append(dirs, dirSource{userRoot, SourceUser})

	for _, ds := range dirs {
		fi, err := os.Stat(ds.dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		entries, err := os.ReadDir(ds.dir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, childName := range names {
			child := filepath.Join(ds.dir, childName)
			cfi, err := os.Stat(child)
			if err != nil || !cfi.IsDir() || strings.HasPrefix(childName, ".") {
				continue
			}
			loophole, err := loadManifest(child)
			if err != nil {
				out = append(out, ValidateEntry{Path: child, Loophole: nil, Err: err.Error()})
				continue
			}
			loophole.Source = ds.source
			out = append(out, ValidateEntry{Path: child, Loophole: loophole, Err: ""})
		}
	}
	return out
}
