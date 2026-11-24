package brokerstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const (
	RoleRead          = "read"
	RoleKnownWrite    = "known-write"
	RoleWithheldWrite = "withheld-write"
)

// OperationResourceKey carries identity in the actual store merge key. Only an
// old known write in the default context retains its legacy bytes.
func OperationResourceKey(role, legacy, context string) (string, error) {
	if role != RoleRead && role != RoleKnownWrite && role != RoleWithheldWrite || legacy == "" || len(legacy) > 65536 || !validContextDigest(context) {
		return "", fmt.Errorf("invalid operation role, resource or context; retry the original call")
	}
	if role == RoleKnownWrite && context == "" {
		return legacy, nil
	}
	raw, _ := json.Marshal([]string{role, legacy, context})
	return "broker-op/1:" + string(raw), nil
}

func (q Request) HasOperationMetadata() bool {
	return q.HasScopedRoles() || q.ContextName != "" || q.ContextDigest != "" || slices.ContainsFunc(q.CallWrites, func(write CallWrite) bool { return write.Role != "" || len(write.Candidates) != 0 })
}

func (q Request) HasScopedRoles() bool {
	if len(q.CallReads) > 0 {
		return true
	}
	return slices.ContainsFunc(q.CallWrites, func(write CallWrite) bool { return write.Role == RoleWithheldWrite })
}

func ValidateOperationBinding(q Request) error {
	if len(q.CallReads)+len(q.CallWrites) > 128 || !validContextDigest(q.ContextDigest) || (q.ContextName == "") != (q.ContextDigest == "") || len(q.ContextName) > 1024 || termsafe.HasUnsafe(q.ContextName) {
		return fmt.Errorf("invalid operation bundle or context identity; retry the original call")
	}
	validate := func(cell string, chain []CallResource) error {
		if !IsCallKind(WorkingCallPrefix+cell) || len(chain) == 0 || len(chain) > 32 {
			return fmt.Errorf("operation has no valid cell or bounded resource chain; retry the original call")
		}
		size := 0
		for _, resource := range chain {
			size += len(resource.Type) + len(resource.ID)
			if resource.Type == "" || resource.ID == "" || termsafe.HasUnsafe(resource.Type+resource.ID) {
				return fmt.Errorf("operation has an invalid resource; retry the original call")
			}
		}
		if size > 65536 {
			return fmt.Errorf("operation resource exceeds the binding budget; shorten the call")
		}
		return nil
	}
	for _, read := range q.CallReads {
		cell := read.Cell
		if cell == "" {
			cell = "read"
		}
		if err := validate(cell, read.Chain); err != nil {
			return err
		}
	}
	for _, write := range q.CallWrites {
		if err := validate(write.Cell, write.Chain); err != nil {
			return err
		}
		if write.Role != "" && write.Role != RoleKnownWrite && write.Role != RoleWithheldWrite || len(write.Candidates) > 64 || len(write.Candidates) != 0 && write.Role != RoleWithheldWrite {
			return fmt.Errorf("invalid write role or candidates; retry the original call")
		}
		seen := map[string]bool{}
		for _, cell := range write.Candidates {
			if !IsCallKind(WorkingCallPrefix+cell) || seen[cell] {
				return fmt.Errorf("invalid or duplicate candidate cell; retry the original call")
			}
			seen[cell] = true
		}
		if len(write.Candidates) > 0 && !seen[write.Cell] {
			return fmt.Errorf("write cell is absent from its candidates; retry the original call")
		}
	}
	return nil
}

// OperationEntries expands only a revalidated bundle, without granting it.
func OperationEntries(q Request) ([]WorkingEntry, error) {
	if err := ValidateOperationBinding(q); err != nil {
		return nil, err
	}
	if q.Workspace == "" || q.Repo == "" || len(q.CallReads)+len(q.CallWrites) == 0 {
		return nil, fmt.Errorf("request names no operation a timed answer can cover; use yolo approve to Allow once")
	}
	var out []WorkingEntry
	appendEntry := func(role, cell string, chain []CallResource) error {
		resource, err := OperationResourceKey(role, CallResourceKey(chain), q.ContextDigest)
		if err != nil {
			return err
		}
		entry := WorkingEntry{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingCallPrefix + cell, Resource: resource, By: "human"}
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
		return nil
	}
	for _, read := range q.CallReads {
		cell := read.Cell
		if cell == "" {
			cell = "read"
		}
		if err := appendEntry(RoleRead, cell, read.Chain); err != nil {
			return nil, err
		}
	}
	for _, write := range q.CallWrites {
		role := write.Role
		if role == "" {
			role = RoleKnownWrite
		}
		cells := write.Candidates
		if len(cells) == 0 {
			cells = []string{write.Cell}
		}
		for _, cell := range cells {
			if err := appendEntry(role, cell, write.Chain); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func validContextDigest(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

// ContextIdentity binds only declared, already-validated plain data. Field
// values feed the digest but never a request, working key or audit record.
func ContextIdentity(source, scope, name string, fields map[string]string) (string, error) {
	if source == "" || name == "" || len(source)+len(scope)+len(name) > 4096 || termsafe.HasUnsafe(source+scope+name) || len(fields) > 64 {
		return "", fmt.Errorf("invalid context identity; select a declared context and retry")
	}
	size := 0
	for key, value := range fields {
		size += len(key) + len(value)
		if key == "" || len(value) > 8192 || termsafe.HasUnsafe(key+value) {
			return "", fmt.Errorf("invalid context field; use declared safe values")
		}
	}
	if size > 65536 {
		return "", fmt.Errorf("context fields exceed the identity budget; shorten the declared values")
	}
	raw, _ := json.Marshal([]any{"broker-context/1", source, scope, name, fields})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// WithWorkingState attaches the current locked snapshot to a temporary request,
// never persisted and never acquired recursively by an adapter.
func (q Request) WithWorkingState(working []WorkingEntry, now time.Time) Request {
	q.currentWorking, q.currentTime, q.hasWorkingState = slices.Clone(working), now, true
	return q
}

func (q Request) WorkingState() ([]WorkingEntry, time.Time, bool) {
	return slices.Clone(q.currentWorking), q.currentTime, q.hasWorkingState
}

func (q Request) WithWorkingStateFrom(other Request) Request {
	q.currentWorking, q.currentTime, q.hasWorkingState = slices.Clone(other.currentWorking), other.currentTime, other.hasWorkingState
	return q
}

func (q Request) WithExecutionContext(ctx context.Context) Request {
	q.executionContext = ctx
	return q
}

func (q Request) ExecutionContext() context.Context {
	if q.executionContext == nil {
		return context.Background()
	}
	return q.executionContext
}

func revalidationRequest(q Request, r *records, now time.Time) Request {
	return q.WithWorkingState(r.working, now)
}
