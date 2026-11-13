package runtime

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestParsePodmanLive(t *testing.T) {
	// Mixed: a running yolo, a stopped yolo (excluded), a non-yolo running
	// (excluded), a paused yolo (included), a short line (skipped).
	stdout := "yolo-app-1234abcd running\n" +
		"yolo-old-5678efgh exited\n" +
		"some-other running\n" +
		"yolo-web-9999zzzz Paused\n" +
		"garbage\n"
	got := keys(ParsePodmanLive(stdout))
	want := []string{"yolo-app-1234abcd", "yolo-web-9999zzzz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePodmanLive = %v, want %v", got, want)
	}
	// Empty stdout -> empty set (NOT unknown; Known is the caller's concern).
	if len(ParsePodmanLive("")) != 0 {
		t.Error("empty stdout should yield empty set")
	}
}

func TestParseContainerLsLive(t *testing.T) {
	stdout := "ID  IMAGE  STATE\n" +
		"yolo-mac-aaaa1111 img running\n" +
		"not-a-jail img running\n"
	got := keys(ParseContainerLsLive(stdout))
	want := []string{"yolo-mac-aaaa1111"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseContainerLsLive = %v, want %v", got, want)
	}
	// Header-only output -> no rows.
	if len(ParseContainerLsLive("ID IMAGE STATE")) != 0 {
		t.Error("header-only should yield no live containers")
	}
}

func TestParseContainerLsImages(t *testing.T) {
	stdout := "ID IMAGE OS ARCH STATE ADDR\n" +
		"yolo-mac-aaaa1111 yolo-jail:0123456789abcdef linux arm64 running 192.168.64.9/24\n" +
		"yolo-ac-builder ghcr.io/x/builder:latest linux arm64 running 192.168.64.10/24\n" +
		"lonely\n"
	got := ParseContainerLsImages(stdout)
	want := []string{"yolo-jail:0123456789abcdef", "ghcr.io/x/builder:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseContainerLsImages = %v, want %v", got, want)
	}
	if len(ParseContainerLsImages("ID IMAGE STATE")) != 0 {
		t.Error("header-only should yield no images")
	}
}

func TestParsePodmanPsRows(t *testing.T) {
	stdout := "yolo-a-1111\tUp 2 hours\t1762437731\n" +
		"yolo-b-2222\tUp 3 minutes\t<no value>\n" + // an unknown start time is the zero time
		"yolo-c-3333\tUp 1 second\t0\n" +
		"short\trow\n" // <3 fields, skipped
	got := ParsePodmanPsRows(stdout)
	want := []PsRow{
		{"yolo-a-1111", "Up 2 hours", time.Unix(1762437731, 0).UTC()},
		{"yolo-b-2222", "Up 3 minutes", time.Time{}},
		{"yolo-c-3333", "Up 1 second", time.Time{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if len(ParsePodmanPsRows("   ")) != 0 {
		t.Error("blank stdout should yield no rows")
	}
}

// containerLsJSONMeasured is the shape `container ls --format json` printed on the Mac
// (docs/lab-reports/2026-11-06-mac-ci-triage-witnesses-run.md §3): top-level keys
// `configuration`, `id` and `status`, with `status` an OBJECT {networks, startedDate, state}
// and startedDate RFC 3339 UTC. Only those keys are measured; nothing else is assumed.
const containerLsJSONMeasured = `[
  {"configuration": {"id": "yolo-su11-ac-f113f9bc"}, "id": "yolo-su11-ac-f113f9bc",
   "status": {"networks": [], "startedDate": "2026-11-06T19:57:12Z", "state": "running"}},
  {"configuration": {"id": "yolo-ac-builder"}, "id": "yolo-ac-builder",
   "status": {"networks": [], "startedDate": "2026-10-09T22:39:57Z", "state": "running"}},
  {"configuration": {"id": "buildkit"}, "id": "buildkit",
   "status": {"networks": [], "startedDate": "2026-10-01T00:00:00Z", "state": "running"}}
]`

// TestParseContainerLsRows: STATUS is the container's state and start time, never the rest
// of the row (lab report 2026-11-04 §7: the table parser joined image, OS, arch, state, IP,
// CPUs, memory and start time into it). Every yolo-* container is listed, as podman's
// `ps --filter name=^yolo-` lists every one, the builder included.
func TestParseContainerLsRows(t *testing.T) {
	got, ok := ParseContainerLsRows(containerLsJSONMeasured)
	if !ok {
		t.Fatal("the measured `container ls --format json` shape did not parse")
	}
	want := []PsRow{
		{Name: "yolo-su11-ac-f113f9bc", Status: "running since 2026-11-06T19:57:12Z",
			Started: time.Date(2026, 11, 6, 19, 57, 12, 0, time.UTC)},
		{Name: "yolo-ac-builder", Status: "running since 2026-10-09T22:39:57Z",
			Started: time.Date(2026, 10, 9, 22, 39, 57, 0, time.UTC)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

// TestParseContainerLsRowsEdges: an empty list is a complete answer (ok, no rows); an output
// that is not the measured JSON is NOT — ps must not read it as "no jails" and prune live
// tracking files (D11). A missing startedDate leaves the bare state; a missing top-level id
// falls back to configuration.id.
func TestParseContainerLsRowsEdges(t *testing.T) {
	if rows, ok := ParseContainerLsRows("[]\n"); !ok || len(rows) != 0 {
		t.Errorf("empty list = %v, %v; want no rows, ok", rows, ok)
	}
	for _, bad := range []string{"", "ID IMAGE OS ARCH STATE\nyolo-x img linux arm64 running\n", "{}"} {
		if _, ok := ParseContainerLsRows(bad); ok {
			t.Errorf("unparseable listing %q read as a complete answer", bad)
		}
	}
	got, ok := ParseContainerLsRows(`[{"configuration": {"id": "yolo-c"}, "status": {"state": "stopped"}}]`)
	want := []PsRow{{Name: "yolo-c", Status: "stopped"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, %v; want %+v", got, ok, want)
	}
}

func TestWorkspaceFromInspectEnv(t *testing.T) {
	env := []string{"PATH=/bin", "YOLO_HOST_DIR=/home/matt/code/thing", "TERM=xterm"}
	if ws, ok := WorkspaceFromInspectEnv(env); !ok || ws != "/home/matt/code/thing" {
		t.Errorf("workspace = %q, %v", ws, ok)
	}
	if _, ok := WorkspaceFromInspectEnv([]string{"PATH=/bin"}); ok {
		t.Error("absent YOLO_HOST_DIR should return ok=false")
	}
	// Value containing '=' is preserved after the first '='.
	if ws, _ := WorkspaceFromInspectEnv([]string{"YOLO_HOST_DIR=/a=b"}); ws != "/a=b" {
		t.Errorf("split-once = %q, want /a=b", ws)
	}
}

func TestBakedYoloVersionFromInspectEnv(t *testing.T) {
	// Present + stripped.
	env := []string{"PATH=/bin", "YOLO_VERSION= 1.2.3 ", "TERM=xterm"}
	if v, ok := BakedYoloVersionFromInspectEnv(env); !ok || v != "1.2.3" {
		t.Errorf("version = %q, %v (want 1.2.3 stripped)", v, ok)
	}
	// Empty-after-strip reads as absent.
	if _, ok := BakedYoloVersionFromInspectEnv([]string{"YOLO_VERSION=   "}); ok {
		t.Error("empty-after-strip should read as absent")
	}
	// Truly absent.
	if _, ok := BakedYoloVersionFromInspectEnv([]string{"PATH=/bin"}); ok {
		t.Error("absent YOLO_VERSION should return ok=false")
	}
}

// TestRenderPsTable: the five columns of named-setups.md §12.3, every one but PACKS padded to
// its widest cell, the header included, with rows in the order given.
func TestRenderPsTable(t *testing.T) {
	rows := []PsTableRow{
		{Workspace: "~/code/notes", Setup: "podman-self", Status: "running", Age: "2d", Packs: "pi"},
		{Workspace: "~/old-thing", Setup: PsUnknownSetup, Status: "starting", Age: "4s", Packs: "?"},
	}
	want := "WORKSPACE     SETUP        STATUS    AGE  PACKS\n" +
		"~/code/notes  podman-self  running   2d   pi\n" +
		"~/old-thing   ?            starting  4s   ?"
	if got := RenderPsTable(rows); got != want {
		t.Errorf("table =\n%q\nwant\n%q", got, want)
	}
	if RenderPsTable(nil) != "" {
		t.Error("no rows should render empty string")
	}
}

func TestStuckReasonFromTop(t *testing.T) {
	// Healthy: a real user command present.
	healthy := "COMMAND\nbash\nclaude\nmise\n"
	if r := StuckReasonFromTop(healthy); r != "" {
		t.Errorf("healthy = %q, want empty", r)
	}
	// Stuck: only provisioning + infra.
	stuck := "COMMAND\nbash\nyolo-entrypo\nmise\nnpm\n"
	if r := StuckReasonFromTop(stuck); r != "stuck in provisioning" {
		t.Errorf("stuck = %q", r)
	}
	// No processes (header only).
	if r := StuckReasonFromTop("COMMAND\n"); r != "no processes" {
		t.Errorf("empty = %q, want 'no processes'", r)
	}
}

// TestResolveRuntimeConfigAware pins the unified listing-command resolver:
// env > config `runtime` key > platform probe, with unknown values ignored.
// Config-awareness is the piece the old DetectRuntime/PsRuntime lacked.
func TestResolveRuntimeConfigAware(t *testing.T) {
	always := func(string) bool { return true }
	cases := []struct {
		name      string
		env, cfg  string
		isMacOS   bool
		hasBinary func(string) bool
		want      string
	}{
		{"config honored on darwin", "", "container", true, always, "container"},
		{"env wins over config", "podman", "container", true, always, "podman"},
		{"linux default", "", "", false, nil, "podman"},
		{"darwin probe prefers AC", "", "", true, func(b string) bool { return b == "container" }, "container"},
		{"darwin falls back to podman when no AC", "", "", true, func(b string) bool { return b == "podman" }, "podman"},
		{"unknown config value ignored", "", "bogus", false, nil, "podman"},
		{"unknown env value ignored", "bogus", "", false, nil, "podman"},
		{"config honored on linux too", "", "container", false, always, "container"},
	}
	for _, tc := range cases {
		if got := ResolveRuntime(tc.env, tc.cfg, tc.isMacOS, tc.hasBinary); got != tc.want {
			t.Errorf("%s: ResolveRuntime(%q,%q,%v) = %q, want %q",
				tc.name, tc.env, tc.cfg, tc.isMacOS, got, tc.want)
		}
	}
}

func TestPodmanMachineResizeHint(t *testing.T) {
	want := "Increase the VM: `podman machine set --memory 4096 && podman machine stop && " +
		"podman machine start`.  Note: this restarts the VM and stops every container running on it."
	if got := PodmanMachineResizeHint(); got != want {
		t.Errorf("hint = %q", got)
	}
}
