package mcpauth

import "encoding/json"

// NativeSourceStatus is the complete public projection of a matching entry.
// Raw entries and credentials never leave the canonical/source reader.
type NativeSourceStatus struct {
	NativeName       string
	ExpiresAtMS      int64
	RefreshPresent   bool
	PendingWriteback bool
}

func ProjectNativeSourceStatus(source OnceSource, raw json.RawMessage, key string, record *Record) NativeSourceStatus {
	refresh, _ := stringAt(raw, source.Fields.RefreshToken)
	status := NativeSourceStatus{ExpiresAtMS: expiryOf(source, raw), RefreshPresent: refresh != ""}
	if source.NativeName != nil {
		status.NativeName = source.NativeName(key, raw)
	}
	if record != nil {
		broker := OnceBroker{}
		status.PendingWriteback = !broker.current(source, raw, record)
	}
	return status
}
