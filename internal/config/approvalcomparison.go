package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ApprovalRecordComparison is a read-only view of the stored workspace approval
// identity and the identity the gate would compute now.
type ApprovalRecordComparison struct {
	RecordPath string
	HasRecord  bool
	Current    string
	Equal      bool
	Malformed  bool
	DiffLines  []string
}

// CompareApprovalRecord reads the same two config identities compared by the gate,
// without invoking the approval path or repairing the stored record.
func CompareApprovalRecord(workspace string) (ApprovalRecordComparison, error) {
	previous, exists, err := ReadApprovalRecord(workspace)
	if err != nil {
		return ApprovalRecordComparison{}, err
	}
	current, err := ApprovalRecordPart(workspace)
	if err != nil {
		return ApprovalRecordComparison{}, err
	}
	result := ApprovalRecordComparison{
		RecordPath: ApprovalSnapshotPath(workspace),
		HasRecord:  exists,
		Current:    current,
	}
	if !exists {
		return result, nil
	}
	decoded, decodeErr := jsonx.Decode([]byte(previous))
	if decodeErr != nil {
		result.Malformed = true
	} else if _, ok := decoded.(*jsonx.OrderedMap); !ok {
		result.Malformed = true
	}
	if !result.Malformed {
		result.Equal = previous == current
	}
	result.DiffLines = unifiedDiff(strings.Split(previous, "\n"), strings.Split(current, "\n"),
		"previous config", "current config")
	return result, nil
}
