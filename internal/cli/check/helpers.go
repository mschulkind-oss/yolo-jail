package check

import (
	"bufio"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// checkPresetNullConflicts reports same-file
// preset/null contradictions (a preset enabled in mcp_presets but null-removed
// in mcp_servers within the SAME config file).
func checkPresetNullConflicts(config *jsonx.OrderedMap, label string) []string {
	var errs []string
	presetsV, _ := config.Get("mcp_presets")
	serversV, _ := config.Get("mcp_servers")
	presets, okP := presetsV.([]any)
	servers, okS := serversV.(*jsonx.OrderedMap)
	if !okP || !okS {
		return errs
	}
	for _, nameV := range presets {
		name, ok := nameV.(string)
		if !ok {
			continue
		}
		if v, present := servers.Get(name); present && v == nil {
			errs = append(errs, label+": preset '"+name+"' is enabled in mcp_presets but "+
				"null-removed in mcp_servers within the same config file")
		}
	}
	return errs
}

// cleanupTrackingFn removes a container's tracking file.
func cleanupTrackingFn(name string) {
	runtime.CleanupContainerTracking(name)
}

// expandUserPath expands a leading "~"/"~/…"
// against $HOME (or the passwd home). A bare "~user" form is left untouched.
func expandUserPath(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i == 1 {
		home := strings.TrimRight(userHome(), "/")
		res := home + p[i:]
		if res == "" {
			return "/"
		}
		return res
	}
	return p
}

func userHome() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		if h == "" {
			return "/"
		}
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return isExecutable(p)
}

// readFileBytes reads a file (used by CDI-spec matching). Wrapper for testing.
func readFileBytes(p string) ([]byte, error) { return os.ReadFile(p) }

// loadConfigLoose returns the user + workspace configs merged, non-strict.
// Any error → nil (the caller substitutes {}).
func loadConfigLoose(workspace string) *jsonx.OrderedMap {
	cfg, err := config.LoadConfig(workspace, false, func(string) {})
	if err != nil {
		return nil
	}
	return cfg
}

// orphanCleanupPrompt runs the y/N prompt in the Running
// Jails block. Returns true iff the user answered y/yes. No Stdin => "N".
func (o *Options) orphanCleanupPrompt(r *reporter, n int) bool {
	prompt := "  " + r.style(pluralOrphansPrompt(n), ansiYellow) + " "
	r.line(prompt)
	if o.Stdin == nil {
		return false
	}
	scanner := bufio.NewScanner(o.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func pluralOrphansPrompt(n int) string {
	return "Stop " + itoa(n) + " orphaned jail(s)? [y/N]"
}

// pyStrOf renders a human string for a non-string cmd[0] element (rare/never
// for real config). Booleans → True/False, numbers → decimal, else JSON.
func pyStrOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloatRepr(t)
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return lit
		}
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}
