package mcpauth

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const testIdentity = "9436f96375b1259ba5c4f8e7c3f9d4b1c0e16a7a66b5ec9d4a0e5e8a6b1f2c3d"

func shared(key string) SharedServer {
	return SharedServer{Server: "linear", Identity: testIdentity, URL: "https://mcp.linear.app/mcp", Key: key, RefreshTokenPath: "refresh_token"}
}

func canonicalRecord(generation uint64, state string, entry string) Record {
	return Record{Server: "linear", Identity: testIdentity, URL: "https://mcp.linear.app/mcp", Generation: generation, State: state, Entry: json.RawMessage(entry)}
}

func TestPlanSeedCreatesTokenlessCopyAndKeepsUnsharedRawEntry(t *testing.T) {
	key := "linear|9436f96375b1259b"
	unshared := json.RawMessage(`{ "z": 1, "a": 2 }`)
	replica := Store{Entries: map[string]json.RawMessage{"notion|aaaa": unshared}, EmptyStore: "delete"}
	out, events := PlanSeed([]Record{canonicalRecord(2, "live", `{"server_name":"linear","access_token":"a","refresh_token":"r"}`)}, replica, []SharedServer{shared(key)})
	if got := string(out.Entries[key]); got != `{"access_token":"a","server_name":"linear"}` {
		t.Fatalf("tokenless copy = %s", got)
	}
	if string(out.Entries["notion|aaaa"]) != string(unshared) {
		t.Fatalf("unshared raw entry changed: %s", out.Entries["notion|aaaa"])
	}
	if len(events) != 1 || events[0].Action != "seed" {
		t.Fatalf("events = %#v", events)
	}
}

func TestPlanSeedReplacesOlderOwnCopyAndDeadRecordRemovesIt(t *testing.T) {
	key := "linear|9436f96375b1259b"
	older := json.RawMessage(`{"access_token":"old"}`)
	out, _ := PlanSeed([]Record{canonicalRecord(3, "live", `{"access_token":"new","refresh_token":"r"}`)}, Store{Entries: map[string]json.RawMessage{key: older}}, []SharedServer{shared(key)})
	if string(out.Entries[key]) != `{"access_token":"new"}` {
		t.Fatalf("newer copy not staged: %s", out.Entries[key])
	}
	out, events := PlanSeed([]Record{canonicalRecord(4, "dead", `{"access_token":"old"}`)}, Store{Entries: map[string]json.RawMessage{key: older}}, []SharedServer{shared(key)})
	if _, ok := out.Entries[key]; ok || len(events) != 1 || events[0].Action != "seed-remove" {
		t.Fatalf("dead record seed = %#v events %#v", out.Entries, events)
	}
}

func TestPlanAdoptNewAndFirstSignInIncrementsAndRemovesRefreshToken(t *testing.T) {
	key := "linear|9436f96375b1259b"
	s := shared(key)
	s.AccessTokenPath = "access_token"
	replica := Store{Entries: map[string]json.RawMessage{key: json.RawMessage(`{"access_token":"new","refresh_token":"rotate"}`)}}
	old := canonicalRecord(5, "live", `{"access_token":"old","refresh_token":"old-refresh"}`)
	records, out, events := PlanAdopt([]Record{old}, replica, true, []SharedServer{s}, "/home/u/work/a")
	if records[0].Generation != 6 || records[0].AdoptedFrom != "/home/u/work/a" || records[0].RefreshToken != "rotate" {
		t.Fatalf("adopted record = %+v", records[0])
	}
	if string(out.Entries[key]) != `{"access_token":"new"}` || len(events) != 1 || events[0].Action != "adopt" {
		t.Fatalf("replica = %s events %#v", out.Entries[key], events)
	}
	records, _, _ = PlanAdopt(nil, replica, true, []SharedServer{s}, "/workspace")
	if len(records) != 1 || records[0].Generation != 1 {
		t.Fatalf("first adoption = %+v", records)
	}
}

func TestPlanAdoptSignoutParseFailureAndUnsharedOpaqueEntry(t *testing.T) {
	key := "linear|9436f96375b1259b"
	record := canonicalRecord(2, "live", `{"access_token":"a"}`)
	replica := Store{Entries: map[string]json.RawMessage{"notion|aaaa": json.RawMessage(`false`)}, EmptyStore: "delete"}
	records, untouched, events := PlanAdopt(nil, replica, true, nil, "/workspace")
	if len(records) != 0 || len(events) != 0 || string(untouched.Entries["notion|aaaa"]) != `false` {
		t.Fatalf("unshared entry affected: %+v %#v", records, events)
	}
	records, untouched, events = PlanAdopt([]Record{record}, Store{Entries: map[string]json.RawMessage{}}, false, []SharedServer{shared(key)}, "/workspace")
	if len(records) != 1 || len(events) != 0 || untouched.Deleted {
		t.Fatalf("parse failure treated as sign-out: %+v %#v", records, events)
	}
	var empty Store
	records, empty, events = PlanAdopt([]Record{record}, Store{Entries: map[string]json.RawMessage{}, EmptyStore: "delete"}, true, []SharedServer{shared(key)}, "/workspace")
	if len(records) != 0 || !empty.Deleted || len(events) != 1 || events[0].Action != "sign-out" {
		t.Fatalf("clean sign-out = %+v %+v %#v", records, empty, events)
	}
	_, keepObject, _ := PlanAdopt([]Record{record}, Store{Entries: map[string]json.RawMessage{}, EmptyStore: "{}"}, true, []SharedServer{shared(key)}, "/workspace")
	if keepObject.Deleted {
		t.Fatal("empty_store={} was planned as deletion")
	}
}

func TestPlanAdoptDoesNotTreatMalformedSharedFieldsAsSignIn(t *testing.T) {
	key := "linear|9436f96375b1259b"
	s := shared(key)
	s.AccessTokenPath = "access_token"
	bad := Store{Entries: map[string]json.RawMessage{key: json.RawMessage(`{"refresh_token":"r"}`)}}
	records, out, events := PlanAdopt(nil, bad, true, []SharedServer{s}, "/workspace")
	if len(records) != 0 || len(events) != 0 || !reflect.DeepEqual(string(out.Entries[key]), string(bad.Entries[key])) {
		t.Fatalf("malformed keyed entry was adopted: %+v %#v", records, events)
	}
}

func TestPlanSeedAndAdoptMalformedRefreshEntryDoNotStallCanonical(t *testing.T) {
	key := "linear|9436f96375b1259b"
	s := shared(key)
	s.AccessTokenPath = "access_token"
	old := canonicalRecord(2, "live", `{"access_token":"canonical","refresh_token":"canonical-refresh"}`)
	for _, state := range []string{"live", "dead"} {
		old.State = state
		replica := Store{Entries: map[string]json.RawMessage{key: json.RawMessage(`{"refresh_token":"r"}`)}}
		for attempt := range 2 {
			seeded, events := PlanSeed([]Record{old}, replica, []SharedServer{s})
			expectedEvents := 1
			if attempt > 0 {
				expectedEvents = 0
			}
			if state == "live" {
				if string(seeded.Entries[key]) != `{"access_token":"canonical"}` || len(events) != expectedEvents || (attempt == 0 && events[0].Action != "seed") {
					t.Fatalf("live malformed seed stalled: %s %#v", seeded.Entries[key], events)
				}
			} else if _, exists := seeded.Entries[key]; exists || len(events) != expectedEvents || (attempt == 0 && events[0].Action != "seed-remove") {
				t.Fatalf("dead malformed seed retained entry: %+v %#v", seeded, events)
			}
			records, adopted, adoptEvents := PlanAdopt([]Record{old}, seeded, true, []SharedServer{s}, "/workspace")
			if len(records) != 1 || records[0].State != state || len(adoptEvents) != 0 {
				t.Fatalf("malformed entry changed canonical: %+v %#v", records, adoptEvents)
			}
			replica = adopted
		}
	}
}

func TestPlanSeedPreservesFreshSignInAgainstLiveAndDeadCanonical(t *testing.T) {
	key := "linear|9436f96375b1259b"
	fresh := json.RawMessage(`{"access_token":"fresh","refresh_token":"new-family"}`)
	s := shared(key)
	s.AccessTokenPath = "access_token"
	for _, state := range []string{"live", "dead"} {
		replica := Store{Entries: map[string]json.RawMessage{key: fresh}}
		out, events := PlanSeed([]Record{canonicalRecord(2, state, `{"access_token":"old","refresh_token":"old-family"}`)}, replica, []SharedServer{s})
		if string(out.Entries[key]) != string(fresh) || len(events) != 0 {
			t.Fatalf("%s: fresh sign-in overwritten: %s events=%v", state, out.Entries[key], events)
		}
	}
}

func TestPlanAdoptKeepsDeadAfterSeedRemovalButSignsOutLiveAbsence(t *testing.T) {
	key := "linear|9436f96375b1259b"
	for _, state := range []string{"dead", "live"} {
		record := canonicalRecord(2, state, `{"access_token":"old"}`)
		seeded, _ := PlanSeed([]Record{record}, Store{Entries: map[string]json.RawMessage{key: json.RawMessage(`{"access_token":"old"}`)}}, []SharedServer{shared(key)})
		if state == "dead" && len(seeded.Entries) != 0 {
			t.Fatalf("dead seed retained copy: %+v", seeded)
		}
		if state == "live" {
			seeded.Entries = map[string]json.RawMessage{} // user signed out after seed
		}
		records, _, events := PlanAdopt([]Record{record}, seeded, true, []SharedServer{shared(key)}, "/workspace")
		if state == "dead" && (len(records) != 1 || records[0].State != "dead" || len(events) != 0) {
			t.Fatalf("dead notice lost: %+v events=%v", records, events)
		}
		if state == "live" && (len(records) != 0 || len(events) != 1 || events[0].Action != "sign-out") {
			t.Fatalf("live sign-out lost: %+v events=%v", records, events)
		}
	}
}

func TestPlanAdoptClearsPreviousRefreshMetadata(t *testing.T) {
	key := "linear|9436f96375b1259b"
	s := shared(key)
	s.AccessTokenPath = "access_token"
	old := canonicalRecord(3, "live", `{"access_token":"old","refresh_token":"old"}`)
	old.TokenEndpoint, old.RefreshedAt = "https://old.example/token", "2026-01-01T00:00:00Z"
	records, replica, _ := PlanAdopt([]Record{old}, Store{Entries: map[string]json.RawMessage{key: json.RawMessage(`{"access_token":"new","refresh_token":"new"}`)}}, true, []SharedServer{s}, "/workspace")
	if records[0].TokenEndpoint != "" || records[0].RefreshedAt != "" || records[0].RefreshToken != "new" {
		t.Fatalf("stale refresh metadata: %+v", records[0])
	}
	if strings.Contains(string(replica.Entries[key]), `"refresh_token"`) {
		t.Fatalf("refresh token leaked to replica: %s", replica.Entries[key])
	}
}
