package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// jsonUnmarshal is a thin wrapper so the inspect-JSON parsers read cleanly.
func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// WorkspaceFromInspectEnv extracts the YOLO_HOST_DIR value from a runtime
// inspect's env lines: scan for the "YOLO_HOST_DIR=" prefix and return the
// value after the first '=' VERBATIM (no strip). Returns ("", false) when
// absent (caller then reports "unknown").
func WorkspaceFromInspectEnv(envLines []string) (string, bool) {
	for _, line := range envLines {
		if strings.HasPrefix(line, "YOLO_HOST_DIR=") {
			return line[len("YOLO_HOST_DIR="):], true
		}
	}
	return "", false
}

// WorkspaceFromContainerInspectJSON parses Apple Container's `container inspect`
// JSON output for the YOLO_HOST_DIR env var (EnvFromContainerInspectJSON reads the
// environment). Returns ("", false) on any parse error or absence.
func WorkspaceFromContainerInspectJSON(stdout string) (string, bool) {
	env, ok := EnvFromContainerInspectJSON(stdout)
	if !ok {
		return "", false
	}
	return WorkspaceFromInspectEnv(env)
}

// EnvFromContainerInspectJSON returns the container's environment, one "K=V" string per entry,
// from Apple Container's `container inspect` output: a JSON document, since that inspect takes
// no --format. The payload measured on 2026-09-16 (docs/plans/setup-support-gaps.md §5.1 row 5)
// is a top-level ARRAY of objects carrying the environment at configuration.initProcess.environment;
// config.env, which this reader's first cut read, is kept as a second place to look. A single
// object is accepted as well as a list. ok is false when the output is not such a document, or
// holds no environment at either place: the caller then knows nothing about the container.
func EnvFromContainerInspectJSON(stdout string) ([]string, bool) {
	type doc struct {
		Configuration struct {
			InitProcess struct {
				Environment []string `json:"environment"`
			} `json:"initProcess"`
		} `json:"configuration"`
		Config struct {
			Env []string `json:"env"`
		} `json:"config"`
	}
	var first doc
	var docs []doc
	if err := jsonUnmarshal(stdout, &docs); err == nil {
		if len(docs) == 0 {
			return nil, false
		}
		first = docs[0]
	} else if err := jsonUnmarshal(stdout, &first); err != nil {
		return nil, false
	}
	if env := first.Configuration.InitProcess.Environment; len(env) > 0 {
		return env, true
	}
	if env := first.Config.Env; len(env) > 0 {
		return env, true
	}
	return nil, false
}

// BakedYoloVersionFromInspectEnv extracts the YOLO_VERSION baked into a
// container's inspect env lines: the value after "YOLO_VERSION=" is STRIPPED,
// and an empty-after-strip value reads as absent. Returns ("", false) when
// absent or empty. The subprocess (inspect --format) stays in the caller; this
// is the pure line parse.
func BakedYoloVersionFromInspectEnv(envLines []string) (string, bool) {
	for _, line := range envLines {
		if strings.HasPrefix(line, "YOLO_VERSION=") {
			v := strings.TrimSpace(line[len("YOLO_VERSION="):])
			if v == "" {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

// PsTableRow is one row of the `yolo ps` table (named-setups.md §12.3), each cell already the
// text it prints: the workspace (`~` for your home), the setup (PsUnknownSetup when not known),
// the status, the age and the packs (each "?" when not known).
type PsTableRow struct {
	Workspace string
	Setup     string
	Status    string
	Age       string
	Packs     string
}

// PsUnknown is the cell of a field that is not known: a jail an older yolo started, which recorded
// none of it (named-setups.md §12.2).
const PsUnknown = "?"

// PsUnknownSetup is the SETUP cell of a row whose setup is not known.
const PsUnknownSetup = PsUnknown

// RenderPsTable renders the `yolo ps` table: the header WORKSPACE SETUP STATUS AGE PACKS, then one
// line per row in the order given, every column but the last left-padded to its widest value
// (measured in Unicode code points), two spaces between columns. Returns "" for no rows (the
// caller prints its empty state instead). No trailing newline on the final row.
func RenderPsTable(rows []PsTableRow) string {
	if len(rows) == 0 {
		return ""
	}
	header := PsTableRow{"WORKSPACE", "SETUP", "STATUS", "AGE", "PACKS"}
	cells := func(r PsTableRow) []string { return []string{r.Workspace, r.Setup, r.Status, r.Age, r.Packs} }
	widths := make([]int, 4)
	for _, r := range append([]PsTableRow{header}, rows...) {
		for i, c := range cells(r)[:4] {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	var b strings.Builder
	for n, r := range append([]PsTableRow{header}, rows...) {
		if n > 0 {
			b.WriteString("\n")
		}
		c := cells(r)
		for i := 0; i < 4; i++ {
			b.WriteString(padRunes(c[i], widths[i]))
			b.WriteString("  ")
		}
		b.WriteString(c[4])
	}
	return b.String()
}

// padRunes left-justifies s to width code points with trailing spaces. If s is
// already >= width runes, it is returned unchanged (never truncated).
func padRunes(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// PodmanMachineMemoryFloorMB is the advisory floor below which Podman Machine
// struggles to host a single jail + one modern agent (claude's first-run native
// install alone has OOM'd at 2 GB).
const PodmanMachineMemoryFloorMB = 4096

// PodmanMachineResizeHint is the single source of truth for the
// `podman machine set` advice, including the VM-restart caveat.
func PodmanMachineResizeHint() string {
	return fmt.Sprintf(
		"Increase the VM: `podman machine set --memory "+
			"%d && podman machine stop && "+
			"podman machine start`.  Note: this restarts the VM and stops "+
			"every container running on it.",
		PodmanMachineMemoryFloorMB,
	)
}

// OOMKillerWarning returns the exit-137-looks-like-OOM hint, or ("", false) when
// the conditions don't fire.
// fire only on macOS + podman + exit 137, when the podman machine's memory is
// below the floor. isMacOS / machineName / machineMemMB are injected (the caller
// runs `podman machine inspect` and supplies memMB<0 / ok=false when the machine
// is unavailable — in which case this returns no warning). 137 isn't only OOM
// (kill -9 also yields it), hence "often means".
func OOMKillerWarning(exitCode int, runtime string, isMacOS bool, machineName string, machineMemMB int, machineOK bool) (string, bool) {
	if !(isMacOS && runtime == "podman" && exitCode == 137) {
		return "", false
	}
	if !machineOK {
		return "", false
	}
	if machineMemMB >= PodmanMachineMemoryFloorMB {
		return "", false
	}
	return fmt.Sprintf(
		"Exit 137 is SIGKILL.  On Podman Machine this often means "+
			"the VM's OOM-killer fired — '%s' has only %d MB "+
			"(below the %d MB recommended floor "+
			"for running an agent).  %s",
		machineName, machineMemMB, PodmanMachineMemoryFloorMB, PodmanMachineResizeHint(),
	), true
}
