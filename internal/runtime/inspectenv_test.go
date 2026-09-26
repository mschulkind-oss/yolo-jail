package runtime

import (
	"slices"
	"testing"
)

// acInspectMeasured is Apple Container's `container inspect <name>` payload in the shape measured
// on 2026-09-16 (docs/plans/setup-support-gaps.md §5.1 row 5): a top-level array, the environment
// under configuration.initProcess.environment. Values are this repo's, trimmed to what a reader
// keys on.
const acInspectMeasured = `[{"id":"yolo-ws-abcd1234",
 "status":{"state":"running","startedDate":"2026-09-16T10:00:00Z","networks":[{"ipv4Address":"192.168.64.3/24","ipv4Gateway":"192.168.64.1"}]},
 "configuration":{"mounts":[],"labels":{},"publishedPorts":[],"publishedSockets":[],"resources":{},
  "initProcess":{"environment":["PATH=/bin:/usr/bin","YOLO_HOST_DIR=/Users/me/ws","YOLO_VERSION=0.10.0"]}}}]`

func TestEnvFromContainerInspectJSON(t *testing.T) {
	want := []string{"PATH=/bin:/usr/bin", "YOLO_HOST_DIR=/Users/me/ws", "YOLO_VERSION=0.10.0"}
	for _, tc := range []struct {
		name, payload string
		want          []string
	}{
		{"the measured array", acInspectMeasured, want},
		{"a bare object", `{"configuration":{"initProcess":{"environment":["A=1"]}}}`, []string{"A=1"}},
		{"config.env, the first cut's reading", `[{"config":{"env":["B=2"]}}]`, []string{"B=2"}},
		{"an empty array", `[]`, nil},
		{"no environment", `[{"configuration":{}}]`, nil},
		{"not JSON: a --format listing", "YOLO_VERSION=0.10.0\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EnvFromContainerInspectJSON(tc.payload)
			if ok != (tc.want != nil) || !slices.Equal(got, tc.want) {
				t.Errorf("EnvFromContainerInspectJSON = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
	if ws, ok := WorkspaceFromContainerInspectJSON(acInspectMeasured); !ok || ws != "/Users/me/ws" {
		t.Errorf("the workspace from the measured payload is %q, %v", ws, ok)
	}
}
