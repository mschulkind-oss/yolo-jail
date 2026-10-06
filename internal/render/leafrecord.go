package render

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// HostLeafWrote is the reader of the host's computed-leaf record (LeafRecordPath, HC-D25) for the
// home at home: it answers whether `yolo host apply` wrote value at pointer in surface
// ("agent/name") and the file still holds exactly that. A launch asks it of a platform switch
// (packload.PlatformSwitchConflicts, PP-D1) to tell a key yolo wrote into the user's own file from
// one the user wrote. No record, no entry for the pointer, or another value answers no, the
// direction that never calls a user's key yolo's.
func HostLeafWrote(home string) func(surface, pointer string, value any) bool {
	return func(surface, pointer string, value any) bool {
		agent, name, ok := strings.Cut(surface, "/")
		if !ok {
			return false
		}
		// OwnershipUnstated, deliberately: the record's path does not depend on the contract
		// (LeafRecordPath sits under ProvenanceDir at the host whatever it is), and a record a
		// retired `assert` apply left is read exactly like one the rmw arm writes under `own`.
		path := Host(home, nil, OwnershipUnstated).LeafRecordPath(agent, name)
		if path == "" {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		wrote, recorded := agentcfg.ParseLeafRecord(data)[pointer]
		if !recorded {
			return false
		}
		a, errA := json.Marshal(jsonx.Plain(value))
		b, errB := json.Marshal(jsonx.Plain(wrote))
		return errA == nil && errB == nil && string(a) == string(b)
	}
}
