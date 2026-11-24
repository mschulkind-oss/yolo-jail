//go:build !linux

package resourcegroups

import "context"

// ChildAgent and ChildBatch mirror the Linux names; no other platform writes them.
const (
	ChildAgent = "yolo-agent"
	ChildBatch = "yolo-batch"
)

// UserIOChildren has no other-platform implementation: every call is the
// unsupported answer, and none touches the host.
type UserIOChildren struct{}

func OpenUserIOChildren(context.Context, string, IOPaths, int, string) (*UserIOChildren, error) {
	return nil, errUnsupportedDelegation
}
func (*UserIOChildren) Close()                  {}
func (*UserIOChildren) ChildPath(string) string { return "" }
func (*UserIOChildren) PlaceProcess(context.Context, string, int) error {
	return errUnsupportedDelegation
}
func ReconcileUserIO(context.Context, string, IOPaths, int, string) error {
	return errUnsupportedDelegation
}
