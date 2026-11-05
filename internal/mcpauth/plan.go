package mcpauth

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// PlanSeed copies live canonical records into their selected replica keys. It does not mutate
// either input. Unshared entries are retained as raw JSON, including their member ordering.
func PlanSeed(records []Record, replica Store, shared []SharedServer) (Store, []Event) {
	out := cloneStore(replica)
	byKey := make(map[string]Record, len(records))
	for _, record := range records {
		byKey[keyFor(record.Server, record.Identity)] = record
	}
	var events []Event
	for _, server := range shared {
		key := server.Key
		if key == "" {
			key = keyFor(server.Server, server.Identity)
		}
		record, ok := byKey[keyFor(server.Server, server.Identity)]
		if !ok {
			continue
		}
		current, exists := out.Entries[key]
		// Only an adoptable refresh-bearing sign-in outranks the canonical copy.
		// A malformed entry cannot be adopted, so seeding must restore/remove it.
		if exists && server.RefreshTokenPath != "" {
			if fields, valid := adoptableFields(current, server); valid && fields.RefreshToken != "" {
				continue
			}
		}
		if record.State == "dead" {
			if _, exists := out.Entries[key]; exists {
				delete(out.Entries, key)
				events = append(events, Event{Action: "seed-remove", Server: server.Server, Generation: record.Generation})
			}
			continue
		}
		copyEntry, err := replicaCopy(record.Entry, server.RefreshTokenPath)
		if err != nil {
			continue
		}
		if !exists || !sameJSON(current, copyEntry) {
			out.Entries[key] = copyEntry
			events = append(events, Event{Action: "seed", Server: server.Server, Generation: record.Generation})
		}
	}
	setEmptyBehavior(&out)
	return out, events
}

// PlanAdopt incorporates sign-ins and cleanly observed sign-outs from a replica. An unreadable
// replica is never interpreted as an empty store.
func PlanAdopt(records []Record, replica Store, parsedOK bool, shared []SharedServer, workspace string) ([]Record, Store, []Event) {
	out := cloneStore(replica)
	recordByKey := make(map[string]int, len(records))
	result := append([]Record(nil), records...)
	for i := range result {
		recordByKey[keyFor(result[i].Server, result[i].Identity)] = i
	}
	var events []Event
	removeRecords := map[string]bool{}
	if !parsedOK {
		return result, out, nil
	}
	for _, server := range shared {
		key := server.Key
		if key == "" {
			key = keyFor(server.Server, server.Identity)
		}
		entry, exists := out.Entries[key]
		recordKey := keyFor(server.Server, server.Identity)
		idx, hasRecord := recordByKey[recordKey]
		if !exists {
			if hasRecord && result[idx].State != "dead" {
				removeRecords[recordKey] = true
				events = append(events, Event{Action: "sign-out", Server: server.Server, Generation: result[idx].Generation})
			}
			continue
		}
		if hasRecord {
			copyEntry, err := replicaCopy(result[idx].Entry, server.RefreshTokenPath)
			if err != nil || sameJSON(entry, copyEntry) {
				continue
			}
		}
		flat, valid := adoptableFields(entry, server)
		if !valid {
			continue
		}
		generation := uint64(1)
		if hasRecord {
			if result[idx].Generation == math.MaxUint64 {
				continue
			}
			generation = result[idx].Generation + 1
		} else {
			idx = len(result)
			recordByKey[recordKey] = idx
			result = append(result, Record{Server: server.Server, Identity: server.Identity, URL: server.URL})
		}
		result[idx].Server = server.Server
		result[idx].Identity = server.Identity
		result[idx].URL = server.URL
		result[idx].Generation = generation
		result[idx].State = "live"
		result[idx].AdoptedFrom = workspace
		result[idx].Entry = append(json.RawMessage(nil), entry...)
		result[idx].AccessToken = flat.AccessToken
		result[idx].RefreshToken = flat.RefreshToken
		result[idx].ExpiresAtMS = flat.ExpiresAtMS
		result[idx].ClientID = flat.ClientID
		result[idx].TokenEndpoint = ""
		result[idx].RefreshedAt = ""
		copyEntry, err := replicaCopy(entry, server.RefreshTokenPath)
		if err == nil {
			out.Entries[key] = copyEntry
		}
		events = append(events, Event{Action: "adopt", Server: server.Server, Generation: generation})
	}
	if len(removeRecords) > 0 {
		kept := result[:0]
		for _, record := range result {
			if !removeRecords[keyFor(record.Server, record.Identity)] {
				kept = append(kept, record)
			}
		}
		result = kept
	}
	setEmptyBehavior(&out)
	return result, out, events
}

type flatFields struct {
	AccessToken  string
	RefreshToken string
	ExpiresAtMS  int64
	ClientID     string
}

func adoptableFields(entry json.RawMessage, server SharedServer) (flatFields, bool) {
	fields := extractFlatFields(entry, server)
	return fields, (server.AccessTokenPath == "" || fields.AccessToken != "") &&
		(server.RefreshTokenPath == "" || fields.RefreshToken != "") &&
		(server.ExpiresAtPath == "" || fields.ExpiresAtMS != 0) &&
		(server.ClientIDPath == "" || fields.ClientID != "")
}

func extractFlatFields(raw json.RawMessage, server SharedServer) flatFields {
	var fields flatFields
	if server.AccessTokenPath != "" {
		fields.AccessToken, _ = stringAt(raw, server.AccessTokenPath)
	}
	if server.RefreshTokenPath != "" {
		fields.RefreshToken, _ = stringAt(raw, server.RefreshTokenPath)
	}
	if server.ClientIDPath != "" {
		fields.ClientID, _ = stringAt(raw, server.ClientIDPath)
	}
	if server.ExpiresAtPath != "" {
		if value, err := rawAt(raw, server.ExpiresAtPath); err == nil {
			fields.ExpiresAtMS, _ = decodeExpiry(value, server.ExpiresAtUnit)
		}
	}
	return fields
}

// StripRefresh removes only the declared refresh credential from a native entry.
func StripRefresh(raw json.RawMessage, refreshPath string) (json.RawMessage, error) {
	return replicaCopy(raw, refreshPath)
}

func replicaCopy(raw json.RawMessage, refreshPath string) (json.RawMessage, error) {
	if refreshPath == "" {
		return append(json.RawMessage(nil), raw...), nil
	}
	entry, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(refreshPath, ".")
	if err := deletePath(entry, parts); err != nil {
		return nil, err
	}
	return marshalRawObject(entry)
}

func deletePath(object map[string]json.RawMessage, path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty JSON path")
	}
	if len(path) == 1 {
		delete(object, path[0])
		return nil
	}
	raw, ok := object[path[0]]
	if !ok {
		return nil
	}
	nested, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if err := deletePath(nested, path[1:]); err != nil {
		return err
	}
	encoded, err := marshalRawObject(nested)
	if err == nil {
		object[path[0]] = encoded
	}
	return err
}

func rawAt(raw json.RawMessage, path string) (json.RawMessage, error) {
	parts := strings.Split(path, ".")
	var current = raw
	for _, part := range parts {
		object, err := decodeObject(current)
		if err != nil {
			return nil, err
		}
		value, ok := object[part]
		if !ok {
			return nil, fmt.Errorf("path %q absent", path)
		}
		current = value
	}
	return current, nil
}

func stringAt(raw json.RawMessage, path string) (string, error) {
	value, err := rawAt(raw, path)
	if err != nil {
		return "", err
	}
	return decodeString(value)
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("entry is not a JSON object")
	}
	return object, nil
}

func cloneStore(in Store) Store {
	out := in
	out.Root = append(json.RawMessage(nil), in.Root...)
	out.Entries = make(map[string]json.RawMessage, len(in.Entries))
	for key, value := range in.Entries {
		out.Entries[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func setEmptyBehavior(store *Store) {
	if len(store.Entries) == 0 {
		switch store.EmptyStore {
		case "delete":
			store.Deleted = true
		case "{}":
			store.Deleted = false
		}
	}
}
