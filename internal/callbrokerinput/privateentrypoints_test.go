package callbrokerinput

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

const fixtureIntegrity = "sha512-RF0Fw+rO5AMf9MAyaRXI4AV0Ulj5lMHqVxxdSgiVbixSCXoEmmX/jk0CuJw4+3SqroYO9VoUh+HcuJivvtJemA=="

func entrypointsFixture(g PreparedHTTPClientGeneration) PreparedHTTPClientEntrypoints {
	plan := g.Clients[0]
	return PreparedHTTPClientEntrypoints{Version: PreparedHTTPClientEntrypointsVersion, Generation: g.Generation,
		Container: g.Container, Loophole: g.Loophole, Clients: []PreparedHTTPClientEntrypoint{{
			Bin: plan.Bin, Pack: plan.Pack, Front: plan.Front, RecipePack: plan.RecipePack, RecipeIdentity: plan.RecipeIdentity,
			Declared: "semver@7.7.2", Platform: "linux/amd64", Runtime: `{"Image":"i","NodeDigest":"3333333333333333333333333333333333333333333333333333333333333333"}`, Compatibility: strings.Repeat("e", 64),
			Release: "7.7.2", SourceURL: "https://registry.npmjs.org/semver/-/semver-7.7.2.tgz", SourceIntegrity: fixtureIntegrity,
			Bootstrap: entrypoint.PrivateClientBootstrap,
			Binding: entrypoint.PrivateClientBinding{Bin: plan.Bin, Generation: g.Generation, CaptureKey: strings.Repeat("f", 16),
				RuntimeRoot: ".local/lib/yolo-npm/x", Entrypoint: ".local/lib/yolo-npm/x/node_modules/semver/bin/semver.js",
				EntrypointSHA256: "sha256:" + strings.Repeat("1", 64), RuntimeFiles: "sha256:" + strings.Repeat("2", 64), NodeSHA256: "sha256:" + strings.Repeat("3", 64)},
		}}}
}

func TestPreparedHTTPClientEntrypointsJoinOnlyTheirPreparedGeneration(t *testing.T) {
	g := preparedHTTPFixture()
	g.Clients[0].RecipeIdentity = "sha256:" + strings.Repeat("9", 64)
	r := entrypointsFixture(g)
	valid, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedHTTPClientEntrypoints(valid)
	if err != nil {
		t.Fatalf("valid record: %v", err)
	}
	if err := ValidatePreparedHTTPClientEntrypoints(decoded, g); err != nil {
		t.Fatalf("valid join: %v", err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"unknown field", strings.TrimSuffix(string(valid), "}") + `,"token":"x"}`},
		{"duplicate field", strings.Replace(string(valid), `"bin":"fixture-client"`, `"bin":"fixture-client","bin":"fixture-client"`, 1)},
		{"case-fold alias", strings.Replace(string(valid), `"release":"7.7.2"`, `"RELEASE":"7.7.2"`, 1)},
		{"trailing", string(valid) + "{}"},
		{"wrong version", strings.Replace(string(valid), PreparedHTTPClientEntrypointsVersion, "yolo.http-client-entrypoints/1", 1)},
		{"no Node identity", strings.Replace(string(valid), `,"node_sha256":"sha256:`+strings.Repeat("3", 64)+`"`, "", 1)},
		{"another Node", strings.Replace(string(valid), `"node_sha256":"sha256:`+strings.Repeat("3", 64)+`"`, `"node_sha256":"sha256:`+strings.Repeat("4", 64)+`"`, 1)},
		{"relative bootstrap", strings.Replace(string(valid), entrypoint.PrivateClientBootstrap, "yolo", 1)},
		{"source fork", strings.Replace(string(valid), "https://registry.npmjs.org/semver", "git+https://example.invalid/semver", 1)},
		{"weak integrity", strings.Replace(string(valid), fixtureIntegrity, "sha1-abc", 1)},
		{"need recipe", strings.Replace(string(valid), `"recipe_identity":"sha256:`, `"recipe_identity":"need:`, 1)},
		{"binding generation", strings.Replace(string(valid), `"generation":"`+g.Generation+`","capture_key"`, `"generation":"`+strings.Repeat("0", 32)+`","capture_key"`, 1)},
		{"binding bin", strings.Replace(string(valid), `"binding":{"bin":"fixture-client"`, `"binding":{"bin":"other"`, 1)},
		{"escaping entrypoint", strings.Replace(string(valid), ".local/lib/yolo-npm/x/node_modules", "../x/node_modules", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodePreparedHTTPClientEntrypoints([]byte(tc.raw)); err == nil {
				t.Fatal("invalid entrypoints record was accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*PreparedHTTPClientGeneration){
		"other generation": func(p *PreparedHTTPClientGeneration) { p.Generation = strings.Repeat("0", 32) },
		"other container":  func(p *PreparedHTTPClientGeneration) { p.Container = "other" },
		"same-key recipe":  func(p *PreparedHTTPClientGeneration) { p.Clients[0].RecipeIdentity = "sha256:other" },
		"unselected bin":   func(p *PreparedHTTPClientGeneration) { p.Clients[0].Bin = "other-client" },
		"extra client": func(p *PreparedHTTPClientGeneration) {
			p.Clients = append(p.Clients, PreparedHTTPClientPlan{Bin: "b", Pack: "p", Front: p.Loophole, RecipePack: "p",
				RecipeIdentity: "sha256:b", TokenVariable: "T", URLVariable: "U", TrustVariable: "R"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			other := preparedHTTPFixture()
			other.Clients[0].RecipeIdentity = g.Clients[0].RecipeIdentity
			mutate(&other)
			if err := ValidatePreparedHTTPClientEntrypoints(decoded, other); err == nil {
				t.Fatal("entrypoints joined a prepared generation they do not belong to")
			}
		})
	}
}
