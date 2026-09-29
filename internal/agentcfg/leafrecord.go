package agentcfg

// leafrecord.go is the codec of the HOST'S COMPUTED-LEAF RECORD (render.Target.LeafRecordPath;
// docs/design/host-computed-layer.md HC-D25): the value yolo last wrote at each leaf a derive
// asserted into a real file through the rmw arm, keyed by the leaf's RFC 6901 pointer
// ("/env/CLAUDE_CODE_USE_BEDROCK"). Two readers need the one spelling: the host apply that
// clears a leaf yolo wrote once its derive stops asserting it, and the launch that tells a
// platform switch yolo wrote in the user's file from one the user wrote (PP-D1).
//
// FAIL-SAFE like the selection record: a record that is absent, unreadable or not a JSON object
// claims nothing, so every leaf then reads as the user's. That is the direction that never
// deletes a value yolo cannot prove it wrote.

import (
	"encoding/json"
	"strings"
)

// ParseLeafRecord decodes a computed-leaf record. Entries whose key is not a pointer ("" or not
// starting with "/") are dropped; nil when nothing survives.
func ParseLeafRecord(data []byte) map[string]any {
	if len(data) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if !strings.HasPrefix(k, "/") {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
