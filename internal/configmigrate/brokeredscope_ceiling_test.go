package configmigrate

import "testing"

// A repository's ceiling (GW-D64) is part of what a user config grants: two configs whose broad
// allowance is the same but whose `[repos.<name>.allow.github]` differs are different grants, so
// the proof names the difference rather than calling them equal (UserBrokerAuthority.Scoped).
func TestTheProofNamesAScopedCeilingDifference(t *testing.T) {
	env(t)
	declarations, err := shippedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	const broad = `"allow": {"github": {"always": ["read"], "on_request": ["create_pr"]}}`
	withoutScoped := mustJSON(t, `{"packs": ["github"], `+broad+`}`)
	withScoped := mustJSON(t, `{"packs": ["github"], `+broad+`, "repos": {"jail": {"repo": "github.com/o/r", "allow": {"github": {"on_request": ["runs"]}}}}}`)
	if diffs := diffValues(brokerGrantsLayer, brokerGrants(withoutScoped, declarations), brokerGrants(withScoped, declarations)); len(diffs) == 0 {
		t.Fatal("a repository ceiling that adds runs was not named as a difference")
	}
	if diffs := diffValues(brokerGrantsLayer, brokerGrants(withScoped, declarations), brokerGrants(withScoped, declarations)); len(diffs) != 0 {
		t.Fatalf("the same scoped config differs from itself: %+v", diffs)
	}
}
