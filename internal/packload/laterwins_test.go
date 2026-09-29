package packload

// laterwins_test.go pins that a SOLE-OWNED claim two packs declare is held by the LATER one in
// the pack order it is handed (config.PackSelection.Packs: config order, then the closure's
// additions, then the local pack), the same "later wins" rule every other key follows
// (docs/plans/notch-convergence.md OQ-NC4 and NC-D59). Each composer used to keep the FIRST.

import (
	"strings"
	"testing"
)

func namedAdapterPack(t *testing.T, name, address string) *Pack {
	t.Helper()
	return &Pack{Name: name, Decl: declFrom(t, `{"contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"`+address+`"}]}`)}
}

// AN ADAPTER PAIR: the later pack's address is the one kept, and it is attributed to that pack.
func TestADuplicatedAdapterPairIsHeldByTheLaterPack(t *testing.T) {
	first := namedAdapterPack(t, "wire-bridge", "http://127.0.0.1:8214")
	later := namedAdapterPack(t, "local", "http://127.0.0.1:9999")
	got := Adaptations([]*Pack{first, later})
	if len(got) != 1 || got[0].Pack != "local" || got[0].Address != "http://127.0.0.1:9999" {
		t.Errorf("Adaptations = %+v, want the one pair, held by the later pack", got)
	}
}

// THE SAME PAIR RESOLVES AT THE LATER PACK'S ADDRESS end to end, through the composed table.
func TestADuplicatedAdapterPairResolvesAtTheLaterPacksAddress(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	packs := []*Pack{agent, vendor,
		namedAdapterPack(t, "first", "https://first.example/anthropic"),
		namedAdapterPack(t, "later", "https://later.example/anthropic")}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	if s := dump(t, providers); !strings.Contains(s, "https://later.example/anthropic") ||
		strings.Contains(s, "https://first.example/anthropic") {
		t.Errorf("the composed table must carry the later pack's adapter address only, got %s", s)
	}
}

// A PROVIDER NAME two packs ship: the later shipper's entry is the table's.
func TestADuplicatedProviderNameIsHeldByTheLaterPack(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://a.example/v4"}}}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://b.example/v4"}}}]}`)}
	s := dump(t, compose(t, nil, []*Pack{a, b}))
	if !strings.Contains(s, "https://b.example/v4") || strings.Contains(s, "https://a.example/v4") {
		t.Errorf("the later shipper must hold the name, got %s", s)
	}
	if p := providerShipper([]*Pack{a, b}, "zai"); p == nil || p.Name != "b" {
		t.Errorf("providerShipper = %v, want the pack whose entry the table holds", p)
	}
	for _, req := range requiredProviders([]*Pack{a, b}, compose(t, nil, []*Pack{a, b})) {
		if req.provider == "zai" && req.pack != "b" {
			t.Errorf("requiredProviders attributes zai to %q, want the later shipper b", req.pack)
		}
	}
}

// A PROFILE NAME two packs ship: the later pack's profile is the one resolved.
func TestADuplicatedProfileNameIsHeldByTheLaterPack(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"fast","provider":"pa"}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"fast","provider":"pb"}]}`)}
	got := packShippedProfiles([]*Pack{a, b})
	if got["fast"].Provider != "pb" {
		t.Errorf("profile fast = %+v, want the later pack's", got["fast"])
	}
}

// A SERVICE NAME two packs declare: the later declaration is the one read by name (the wire
// bridge's endpoint file, serviceEndpointEnvArgs in the run pipeline).
func TestADuplicatedServiceNameIsHeldByTheLaterPack(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"wire-bridge","endpoint":"a.endpoint","jail_daemon":{"cmd":["yolo-jaild","a"]}}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"wire-bridge","endpoint":"b.endpoint","jail_daemon":{"cmd":["yolo-jaild","b"]}}]}`)}
	got, ok := ServiceNamed([]*Pack{a, b}, "wire-bridge")
	if !ok || got.Endpoint != "b.endpoint" {
		t.Errorf("ServiceNamed = %+v (%v), want the later pack's", got, ok)
	}
	if _, ok := ServiceNamed([]*Pack{a, b}, "absent"); ok {
		t.Error("a name no pack declares must not be found")
	}
}

// THE SAME NAME, EVERY READER: HeldServices keeps the later declaration and records the
// earlier one as shadowed by it (the launch's disclosure reads that record); the payload's
// name list carries the name once; and `yolo pack footprint` (Collisions) reports the pair.
func TestADuplicatedServiceNameIsShadowedForEveryReader(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"wire-bridge","endpoint":"a.endpoint","jail_daemon":{"cmd":["yolo-jaild","a"]}}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"wire-bridge","endpoint":"b.endpoint","jail_daemon":{"cmd":["yolo-jaild","b"]}},
	  {"kind":"service","name":"other","jail_daemon":{"cmd":["yolo-jaild","other"]}}]}`)}
	held, shadowed := HeldServices([]*Pack{a, b})
	if len(held) != 2 || held[0].Pack != "b" || held[0].Service.Endpoint != "b.endpoint" ||
		held[1].Service.Name != "other" {
		t.Errorf("held = %+v, want b's wire-bridge then b's other", held)
	}
	if len(shadowed) != 1 || shadowed[0] != (ShadowedService{Name: "wire-bridge", Pack: "a", HeldBy: "b"}) {
		t.Errorf("shadowed = %+v, want a's wire-bridge shadowed by b", shadowed)
	}
	if got := strings.Join(ServiceJailDaemonNames([]*Pack{a, b}), ","); got != "other,wire-bridge" {
		t.Errorf("ServiceJailDaemonNames = %q, want each name once", got)
	}
	found := false
	for _, c := range Collisions([]*Pack{a, b}) {
		if c.Target == "wire-bridge" && strings.Join(c.Packs, ",") == "a,b" {
			found = true
		}
	}
	if !found {
		t.Errorf("Collisions must report the service name both packs declare: %+v", Collisions([]*Pack{a, b}))
	}
}

// A HOLDER WITH NO DAEMON runs none: the earlier pack's jail_daemon is not a fallback, since
// the endpoint the launch points at is the holder's.
func TestAServiceHeldWithoutADaemonRunsNone(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"svc","jail_daemon":{"cmd":["yolo-jaild","a"]}}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"service","name":"svc","host_daemon":{"cmd":["svc-host"]}}]}`)}
	if got := ServiceJailDaemonNames([]*Pack{a, b}); len(got) != 0 {
		t.Errorf("ServiceJailDaemonNames = %v, want none: the holder declares no daemon", got)
	}
}
