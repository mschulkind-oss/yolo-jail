package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestPrivateProgramEnvStaysOutOfOrdinaryFoldAndProfileServedIndex(t *testing.T) {
	manifest, problems := packdecl.Decode([]byte(`{"name":"fixture","contributes":[
		{"kind":"program","bin":"fixture-agent","via":"npm","package":"fixture-agent"},
		{"kind":"env","bin":"fixture-http-client","served_by":"fixture-http","vars":{"SSL_CERT_FILE":"private-ca","PRIVATE_ONLY":"{caller_token}"}},
		{"kind":"env","served_by":"ordinary-service","vars":{"SSL_CERT_FILE":"ordinary-ca","ORDINARY":"value"}},
		{"kind":"env","profile":"fixture-profile","served_by":"profile-service","vars":{"PROFILE_ONLY":"profile-value"}},
		{"kind":"env","platform":"aws-bedrock","served_by":"platform-service","vars":{"PLATFORM_ONLY":"platform-value"}}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("fixture manifest: %v", problems)
	}
	packs := []*Pack{{Name: "fixture", Decl: manifest}}
	unselected := UnselectedProfileServedDaemons(packs, GateSelection{})
	if len(unselected) != 2 || unselected[0].Name != "platform-service" || unselected[1].Name != "profile-service" {
		t.Fatalf("private env became a profile-served daemon, or ordinary gates changed: %+v", unselected)
	}
	selection := GateSelection{Profiles: map[string]string{"fixture-agent": "fixture-profile"}, Platforms: map[string]string{"fixture-agent": "aws-bedrock"}}
	fold := EnvFold(packs, selection, "fixture-agent")
	got := map[string]EnvFoldEntry{}
	for _, entry := range fold {
		got[entry.Key] = entry
	}
	if len(got) != 4 || got["SSL_CERT_FILE"].Value != "ordinary-ca" || got["SSL_CERT_FILE"].ServedBy != "ordinary-service" ||
		got["ORDINARY"].Value != "value" || got["PROFILE_ONLY"].Value != "profile-value" || got["PLATFORM_ONLY"].Value != "platform-value" {
		t.Fatalf("ordinary fold includes private env or loses unrelated gates: %+v", got)
	}
	if _, ok := got["PRIVATE_ONLY"]; ok {
		t.Fatal("private client placeholder entered the ordinary environment fold")
	}
	if got := UnselectedProfileServedDaemons(packs, selection); len(got) != 0 {
		t.Fatalf("selected ordinary profile/platform gates changed: %+v", got)
	}
}

func TestResolvePrivateProgramEnvUsesSelectedRecipeAndApprovedHTTPFront(t *testing.T) {
	client := &Pack{Name: "client-pack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindEnv, Bin: "fixture-http-client", ServedBy: "fixture-http",
		Vars: map[string]string{"FIXTURE_API_TOKEN": "{caller_token}", "FIXTURE_BASE_URL": "https://{listen}", "SSL_CERT_FILE": "{client_trust}"},
	}}}}
	program := &Pack{Name: "program-pack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "fixture-http-client", Via: "npm", Package: "fixture-http-client",
	}}}}
	plans, err := ResolvePrivateProgramEnv([]*Pack{client, program}, "fixture-http", "fixture-pack", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one", plans)
	}
	plan := plans[0]
	if plan.Bin != "fixture-http-client" || plan.Pack != "client-pack" || plan.RecipePack != "program-pack" || plan.RecipeIdentity == "" ||
		plan.Front != "fixture-http" || plan.TokenVariable != "FIXTURE_API_TOKEN" || plan.URLVariable != "FIXTURE_BASE_URL" || plan.TrustVariable != "SSL_CERT_FILE" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestResolvePrivateProgramEnvRefusesMissingOrAmbiguousTargets(t *testing.T) {
	client := &Pack{Name: "client-pack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindEnv, Bin: "fixture-http-client", ServedBy: "fixture-http",
		Vars: map[string]string{"TOKEN": "{caller_token}", "URL": "https://{listen}", "TRUST": "{client_trust}"},
	}}}}
	for _, tc := range []struct {
		name  string
		packs []*Pack
		front string
		ok    bool
	}{
		{"no approved front", []*Pack{client}, "fixture-http", false},
		{"no recipe or need", []*Pack{client}, "fixture-http", true},
		{"colliding recipes", []*Pack{client,
			{Name: "recipe-a", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindProgram, Bin: "fixture-http-client", Via: "npm", Package: "a"}}}},
			{Name: "recipe-b", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindProgram, Bin: "fixture-http-client", Via: "npm", Package: "b"}}}},
		}, "fixture-http", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolvePrivateProgramEnv(tc.packs, tc.front, "fixture-http", tc.ok)
			if err == nil {
				t.Fatal("unresolved or ambiguous private client was accepted")
			}
		})
	}
}
