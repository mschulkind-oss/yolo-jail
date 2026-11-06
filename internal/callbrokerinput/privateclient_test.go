package callbrokerinput

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func preparedHTTPFixture() PreparedHTTPClientGeneration {
	return PreparedHTTPClientGeneration{
		Version: PreparedHTTPClientGenerationVersion, Phase: "prepared", Generation: strings.Repeat("a", 32),
		Container: "fixture-container", FrontJailID: "fixture-front", Runtime: "podman", Setup: "podman-self",
		PackTree: "/host/pack-tree", LaunchID: strings.Repeat("b", 16), Loophole: "fixture-http", Front: "http",
		Pack: "fixture-pack", Source: "fixture-source", DeclarationHash: strings.Repeat("c", 64),
		Roots: []string{}, ProgramIdentities: map[string]loopholedecl.CallBrokerProgramIdentity{
			"classifier": {Pack: "client-pack", Bin: "classifier", Path: "/bin/classifier", RelPath: "classifier", SHA256: strings.Repeat("d", 64)},
		},
		Clients: []PreparedHTTPClientPlan{{Bin: "fixture-client", Pack: "client-pack", Front: "fixture-http",
			RecipePack: "client-pack", RecipeIdentity: "sha256:fixture", TokenVariable: "TOKEN",
			URLVariable: "URL", TrustVariable: "TRUST"}},
	}
}

func TestDecodePreparedHTTPClientGenerationStrictPreLeaseSchema(t *testing.T) {
	valid, err := json.Marshal(preparedHTTPFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePreparedHTTPClientGeneration(valid); err != nil {
		t.Fatalf("valid prepared identity: %v", err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"unknown future state", strings.TrimSuffix(string(valid), "}") + `,"broker_start":"live"}`},
		{"wrong version", strings.Replace(string(valid), PreparedHTTPClientGenerationVersion, "yolo.http-client-prepared/2", 1)},
		{"wrong phase", strings.Replace(string(valid), `"phase":"prepared"`, `"phase":true`, 1)},
		{"missing generation", strings.Replace(string(valid), `"generation":"`+strings.Repeat("a", 32)+`",`, "", 1)},
		{"duplicate top-level", strings.TrimSuffix(string(valid), "}") + `,"phase":"prepared"}`},
		{"duplicate nested", strings.Replace(string(valid), `"bin":"fixture-client"`, `"bin":"fixture-client","bin":"other"`, 1)},
		{"case-fold top-level alias", strings.Replace(string(valid), `"phase":"prepared"`, `"PHASE":"prepared"`, 1)},
		{"case-fold phase overwrite", strings.Replace(string(valid), `"phase":"prepared"`, `"phase":"ready","PHASE":"prepared"`, 1)},
		{"case-fold nested alias", strings.Replace(string(valid), `"token_variable":"TOKEN"`, `"TOKEN_VARIABLE":"TOKEN"`, 1)},
		{"case-fold nested overwrite", strings.Replace(string(valid), `"token_variable":"TOKEN"`, `"token_variable":"OTHER","TOKEN_VARIABLE":"TOKEN"`, 1)},
		{"case-fold identity alias", strings.Replace(string(valid), `"Pack":"client-pack"`, `"Pack":"client-pack","pack":"other"`, 1)},
		{"null roots", strings.Replace(string(valid), `"roots":[]`, `"roots":null`, 1)},
		{"wrong nested type", strings.Replace(string(valid), `"token_variable":"TOKEN"`, `"token_variable":1`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodePreparedHTTPClientGeneration([]byte(tc.raw)); err == nil {
				t.Fatal("invalid prepared identity was accepted")
			}
		})
	}
}

func TestLaunchHTTPFrontGenerationReferenceRejectsOnlyItsDuplicate(t *testing.T) {
	if err := RejectDuplicateLaunchHTTPFrontGeneration([]byte(`{"version":"v2","other":{"duplicate":1,"duplicate":2}}`)); err != nil {
		t.Fatalf("legacy unrelated duplicate behavior changed: %v", err)
	}
	if err := RejectDuplicateLaunchHTTPFrontGeneration([]byte(`{"http_front_generation":"a","http_front_generation":"b"}`)); err == nil {
		t.Fatal("duplicate prepared-generation reference was accepted")
	}
}
