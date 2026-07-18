// Command yolo-parity dumps Go-side constants and pure-function outputs as a
// single canonical JSON document, byte-identical to what
// tools/parity/py_drift_dump.py emits from the live Python. A fast-suite
// pytest (tests/test_go_drift.py) byte-diffs the two on every commit inside
// `just check-ci`, so any Python change without a matching Go change is a red
// build — the port's continuous cross-session safety net (go-port plan §5.3).
//
// This binary is deleted at cutover (Stage 17) along with the drift suite.
//
// The document MUST stay byte-identical to the Python dump: same keys, same
// values, same canonical JSON serialization (indent=2, sort_keys,
// ensure_ascii + trailing newline). Add a key here AND to py_drift_dump.py in
// the same commit or the drift test goes red — which is the point.
package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/naming"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// om is a tiny helper to build an ordered map from key/value pairs.
func om(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func strAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// orNull returns nil (JSON null) for an empty string — the Go representation
// of a Python None field, so the dump matches py_drift_dump.py.
func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pathsSection() *jsonx.OrderedMap {
	return om(
		"SUPPORTED_RUNTIMES", strAny(paths.SupportedRuntimes),
		"NATIVE_RUNTIMES", strAny(paths.NativeRuntimes),
		"ALL_RUNTIMES", strAny(paths.AllRuntimes),
		"JAIL_IMAGE", paths.JailImage,
		"JAIL_IMAGE_SHORT", paths.JailImageShort,
		"JAIL_HOST_SERVICES_DIR", paths.JailHostServicesDir,
		"BUILTIN_CGROUP_LOOPHOLE_NAME", paths.BuiltinCgroupLoopholeName,
		"BUILTIN_JOURNAL_LOOPHOLE_NAME", paths.BuiltinJournalLoopholeName,
		"JOURNAL_SOCKET_NAME", paths.JournalSocketName,
		"CGD_SOCKET_NAME", paths.CgdSocketName,
		"GLOBAL_STORAGE_SUFFIX", ".local/share/yolo-jail",
		"USER_CONFIG_SUFFIX", ".config/yolo-jail/config.jsonc",
	)
}

// versionCorpus MUST match _version_normalizations() in py_drift_dump.py.
var versionCorpus = []string{
	"0.1.0",
	"v0.1.0",
	"0.1.0-dirty",
	"v0.1.0-dirty",
	"0.1.0-3-gabcdef1",
	"0.1.0-3-gabcdef1-dirty",
	"v0.6.0-19-g661ac98",
	"1.2.3-rc1",
	"deadbeef",
	"deadbeef-dirty",
}

func versionSection() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for _, raw := range versionCorpus {
		m.Set(raw, version.Normalize(raw))
	}
	return m
}

// containerNameCorpus MUST match _container_name_cases() in py_drift_dump.py
// (already-resolved absolute paths).
var containerNameCorpus = []string{
	"/home/matt/code/system/yolo-jail",
	"/srv/App",
	"/srv/two words & punctuation!",
	"/srv/dir.with.dots",
	"/srv/CAP-Mixed_Case",
	"/srv/café-münchen",
	"/srv/" + repeat("x", 60),
	"/srv/---",
	"/",
}

func containerNamesSection() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for _, p := range containerNameCorpus {
		m.Set(p, naming.FromResolved(p))
	}
	return m
}

func agentSpecDict(s agents.AgentSpec) *jsonx.OrderedMap {
	return om(
		"name", s.Name,
		"install", om(
			"kind", s.Install.Kind,
			"bin", s.Install.Bin,
			"package", orNull(s.Install.Package),
			"install_flags", strAny(s.Install.InstallFlags),
			"installer_url", orNull(s.Install.InstallerURL),
		),
		"config_writer", s.ConfigWriter,
		"briefing", om(
			"staging", s.Briefing.Staging,
			"mount", s.Briefing.Mount,
			"host_source", s.Briefing.HostSource,
		),
		"overlay_dirs", strAny(s.OverlayDirs),
		"skills", orNull(s.Skills),
		"skills_staging", orNull(s.SkillsStaging()),
		"yolo_flags", strAny(s.YoloFlags),
		"alias", orNull(s.Alias),
		"mise_retire", strAny(s.MiseRetire),
	)
}

func agentsSection() *jsonx.OrderedMap {
	specs := jsonx.NewOrderedMap()
	for _, name := range agents.Order {
		s, _ := agents.Get(name)
		specs.Set(name, agentSpecDict(s))
	}
	return om(
		"order", strAny(agents.Order),
		"specs", specs,
		"DEFAULT_AGENTS", strAny(agents.DefaultAgents),
		"VALID_AGENTS", strAny(agents.ValidAgents),
		"ALL_MISE_RETIRE", strAny(agents.AllMiseRetire),
		"ALL_OVERLAY_DIRS", strAny(agents.AllOverlayDirs),
	)
}

func buildDump() *jsonx.OrderedMap {
	return om(
		"paths", pathsSection(),
		"version_normalizations", versionSection(),
		"container_names", containerNamesSection(),
		"agents", agentsSection(),
	)
}

func main() {
	out, err := jsonx.DumpsSnapshot(buildDump())
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo-parity:", err)
		os.Exit(1)
	}
	fmt.Println(out) // trailing newline, matching Python's dump
}

func repeat(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for range n {
		b = append(b, s...)
	}
	return string(b)
}
