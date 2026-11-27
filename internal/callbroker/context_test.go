package callbroker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The context is declaration-owned: these tests run it for a source that is not AWS, with
// variables no shipped pack names, so a branch on a source or variable name in core fails
// them (F13). Nothing here touches a credential file, `sts`, an SDK or a real `aws`.

const fixtureAnchor = "x://"

func fixtureDeclaredContext() *loopholedecl.CallBrokerContext {
	return &loopholedecl.CallBrokerContext{
		Fields:       map[string]string{"account": "FIXTURE_ACCOUNT"},
		Unset:        []string{"FIXTURE_STATIC_A", "FIXTURE_STATIC_B"},
		RouteAnchors: []string{fixtureAnchor},
	}
}

func fixtureRouting(t *testing.T, routes []brokerpolicy.Route, contexts ...config.BrokerExecutionContext) config.BrokerRouting {
	t.Helper()
	matcher, err := brokerpolicy.NewScopeMatcher("segment", "/", []string{fixtureAnchor})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := brokerpolicy.NewPolicy("fixture", matcher, []brokerpolicy.Cell{{Name: "list", Kind: brokerpolicy.Read}}, true, false, nil, nil, routes)
	if err != nil {
		t.Fatal(err)
	}
	routing := config.BrokerRouting{Policy: policy, Contexts: map[string]config.BrokerExecutionContext{}}
	for _, c := range contexts {
		c.Source, c.Selected = "fixture", true
		routing.Contexts[c.Name] = c
	}
	return routing
}

func fixtureContext(name, account string) config.BrokerExecutionContext {
	return config.BrokerExecutionContext{Name: name, Fields: map[string]string{"account": account}}
}

func fixtureOp(ids ...string) brokerpolicy.Operation {
	chain := make([]brokerpolicy.Resource, len(ids))
	for i, id := range ids {
		chain[i] = brokerpolicy.Resource{Type: "object", ID: id}
	}
	return brokerpolicy.Operation{Source: "fixture", Kind: brokerpolicy.Read, Cell: "list", Chain: chain}
}

func fixtureRoute(scope, name string) brokerpolicy.Route {
	return brokerpolicy.Route{Source: "fixture", Scope: scope, Context: name, Origin: "scopes.fixture"}
}

// fakeHostProgram writes a program named `fixture-host` that records its environment and
// prints FIXTURE_ACCOUNT, and pins it as the executor would.
func fakeHostProgram(t *testing.T, captured ...string) (pinnedCLI, string) {
	t.Helper()
	dir := shortCallBrokerTempDir(t)
	record := filepath.Join(dir, "env-record")
	script := "#!/bin/sh\nenv | sort > '" + record + "'\nprintf 'account=%s\\n' \"$FIXTURE_ACCOUNT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "fixture-host"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	program, err := pinCLI("fixture-host", captured)
	if err != nil {
		t.Fatal(err)
	}
	return program, record
}

func runFixture(t *testing.T, program pinnedCLI, rc *routedContext) CLIResult {
	t.Helper()
	call := Classification{Argv: []string{"list"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}
	result, err := program.run(withRoutedContext(context.Background(), rc), call, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("fixture host program: %+v %v", result, err)
	}
	return result
}

func recordedEnv(t *testing.T, record string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if name, value, found := strings.Cut(line, "="); found {
			env[name] = value
		}
	}
	return env
}

// F1, F2, F3, F13: the routed call sees the declared variable and none of the unset
// names, the default call is byte for byte the captured environment, and the pinned
// environment is never mutated by either.
func routedEditsACopy(t *testing.T) {
	t.Setenv("FIXTURE_STATIC_A", "static-a")
	t.Setenv("FIXTURE_STATIC_B", "static-b")
	t.Setenv("FIXTURE_ACCOUNT", "host-account")
	t.Setenv("FIXTURE_KEPT", "kept")
	program, record := fakeHostProgram(t, "FIXTURE_STATIC_A", "FIXTURE_STATIC_B", "FIXTURE_ACCOUNT", "FIXTURE_KEPT")
	pinned := append([]string(nil), program.env...)

	rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(),
		fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://a/team/", "finance")}, fixtureContext("finance", "finance-ro")),
		[]brokerpolicy.Operation{fixtureOp("x://a/team/f", "x://a")})
	if err != nil || rc == nil || rc.Name != "finance" || rc.Digest == "" {
		t.Fatalf("route not selected: %+v %v", rc, err)
	}
	runFixture(t, program, rc)
	routed := recordedEnv(t, record)
	if routed["FIXTURE_ACCOUNT"] != "finance-ro" {
		t.Fatalf("declared variable not set from the context field: %v", routed)
	}
	for _, name := range []string{"FIXTURE_STATIC_A", "FIXTURE_STATIC_B"} {
		if _, present := routed[name]; present {
			t.Fatalf("%s survived the declared unset on a routed call", name)
		}
	}
	if routed["FIXTURE_KEPT"] != "kept" || routed["HOME"] != os.Getenv("HOME") || routed["PATH"] != os.Getenv("PATH") {
		t.Fatalf("other captured names must pass through: %v", routed)
	}

	// F2/F3: no route means the captured environment, static names winning as today.
	runFixture(t, program, nil)
	plain := recordedEnv(t, record)
	if plain["FIXTURE_STATIC_A"] != "static-a" || plain["FIXTURE_ACCOUNT"] != "host-account" {
		t.Fatalf("the default context changed: %v", plain)
	}
	if strings.Join(program.env, "\x00") != strings.Join(pinned, "\x00") {
		t.Fatal("a routed call mutated the pinned captured environment")
	}
}

// F14: values reach the program verbatim, with no trimming or normalization.
func routedValuesVerbatim(t *testing.T) {
	program, record := fakeHostProgram(t)
	for _, value := range []string{"team prod", "équipe-ro", " leading and trailing ", strings.Repeat("a", 1024)} {
		rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(),
			fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://", "ctx")}, fixtureContext("ctx", value)),
			[]brokerpolicy.Operation{fixtureOp("x://a")})
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		runFixture(t, program, rc)
		if got := recordedEnv(t, record)["FIXTURE_ACCOUNT"]; got != value {
			t.Fatalf("value changed on the way: %q != %q", got, value)
		}
	}
}

// The context's field values are redacted from output, like every other declared value.
func routedValuesRedacted(t *testing.T) {
	program, _ := fakeHostProgram(t)
	rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(),
		fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://", "ctx")}, fixtureContext("ctx", "private selector")),
		[]brokerpolicy.Operation{fixtureOp("x://a")})
	if err != nil {
		t.Fatal(err)
	}
	out := string(runFixture(t, program, rc).Stdout)
	if strings.Contains(out, "private selector") || !strings.Contains(out, "[redacted]") {
		t.Fatalf("context value not redacted: %q", out)
	}
	if len(program.secrets) != 0 {
		t.Fatal("redaction must not add a context value to the pinned program's own list")
	}
}

// F4: the most specific route selects; the source scope is least specific.
func routedMostSpecificFirst(t *testing.T) {
	routing := fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://a/", "bucket"), fixtureRoute("x://a/team/", "team"), fixtureRoute("x://", "anchor")},
		fixtureContext("bucket", "b"), fixtureContext("team", "t"), fixtureContext("anchor", "n"))
	for _, tc := range []struct {
		name, want string
		op         brokerpolicy.Operation
	}{
		{"nested prefix wins", "team", fixtureOp("x://a/team/f", "x://a/team/", "x://a")},
		{"bucket route", "bucket", fixtureOp("x://a/other/f", "x://a/other/", "x://a")},
		{"anchor-wide route covers an unrouted bucket", "anchor", fixtureOp("x://z/f", "x://z")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(), routing, []brokerpolicy.Operation{tc.op})
			if err != nil || rc == nil || rc.Name != tc.want {
				t.Fatalf("selected %+v %v want %s", rc, err, tc.want)
			}
		})
	}
	if rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(), fixtureRouting(t, nil), []brokerpolicy.Operation{fixtureOp("x://z/f")}); rc != nil || err != nil {
		t.Fatalf("no route must be the default context: %+v %v", rc, err)
	}
}

// A source whose declaration has no context block never reads or applies a route.
func undeclaredBlockIsDefault(t *testing.T) {
	routing := fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://", "ctx")}, fixtureContext("ctx", "a"))
	if rc, err := selectRoutedContext("fixture", "decl-hash", nil, routing, []brokerpolicy.Operation{fixtureOp("x://a")}); rc != nil || err != nil {
		t.Fatalf("a context ran for a declaration with no block: %+v %v", rc, err)
	}
}

// 7.2: operations selecting different contexts refuse; a matched route whose context
// cannot be built fails and is never the default context.
func routedRefusals(t *testing.T) {
	routes := []brokerpolicy.Route{fixtureRoute("x://a/", "one"), fixtureRoute("x://b/", "two")}
	declared := fixtureDeclaredContext()
	for _, tc := range []struct {
		name    string
		routing config.BrokerRouting
		ops     []brokerpolicy.Operation
		want    string
	}{
		{"different contexts", fixtureRouting(t, routes, fixtureContext("one", "1"), fixtureContext("two", "2")),
			[]brokerpolicy.Operation{fixtureOp("x://a/f", "x://a"), fixtureOp("x://b/f", "x://b")}, "different execution contexts"},
		{"routed beside unrouted", fixtureRouting(t, routes, fixtureContext("one", "1"), fixtureContext("two", "2")),
			[]brokerpolicy.Operation{fixtureOp("x://a/f", "x://a"), fixtureOp("x://c/f", "x://c")}, "different execution contexts"},
		{"missing context", fixtureRouting(t, routes), []brokerpolicy.Operation{fixtureOp("x://a/f", "x://a")}, "not run in the default context"},
		{"missing field", fixtureRouting(t, routes, config.BrokerExecutionContext{Name: "one", Fields: map[string]string{}}),
			[]brokerpolicy.Operation{fixtureOp("x://a/f", "x://a")}, "no value for declared field"},
		{"undeclared field", fixtureRouting(t, routes, config.BrokerExecutionContext{Name: "one", Fields: map[string]string{"account": "1", "region": "r"}}),
			[]brokerpolicy.Operation{fixtureOp("x://a/f", "x://a")}, "undeclared field"},
		{"invalid value", fixtureRouting(t, routes, fixtureContext("one", "a\nb")),
			[]brokerpolicy.Operation{fixtureOp("x://a/f", "x://a")}, "control"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, err := selectRoutedContext("fixture", "decl-hash", declared, tc.routing, tc.ops)
			if err == nil || rc != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rc=%+v err=%v want %s", rc, err, tc.want)
			}
		})
	}
}

// F11 and 7.4: the identity binds the declaration hash (and so the unset rule), the
// context name, every field value and the winning scope bytes; it is a digest, so it
// carries no field value.
func routedIdentityBinds(t *testing.T) {
	identity := func(hash string, routes []brokerpolicy.Route, ctx config.BrokerExecutionContext, ids ...string) string {
		rc, err := selectRoutedContext("fixture", hash, fixtureDeclaredContext(), fixtureRouting(t, routes, ctx), []brokerpolicy.Operation{fixtureOp(ids...)})
		if err != nil || rc == nil {
			t.Fatalf("%v %+v", err, rc)
		}
		return rc.Digest
	}
	base := identity("h1", []brokerpolicy.Route{fixtureRoute("x://a/", "n")}, fixtureContext("n", "v"), "x://a/f", "x://a")
	for name, other := range map[string]string{
		"declaration hash (set/unset rule)": identity("h2", []brokerpolicy.Route{fixtureRoute("x://a/", "n")}, fixtureContext("n", "v"), "x://a/f", "x://a"),
		"field value":                       identity("h1", []brokerpolicy.Route{fixtureRoute("x://a/", "n")}, fixtureContext("n", "w"), "x://a/f", "x://a"),
		"context name":                      identity("h1", []brokerpolicy.Route{fixtureRoute("x://a/", "m")}, fixtureContext("m", "v"), "x://a/f", "x://a"),
		"winning scope":                     identity("h1", []brokerpolicy.Route{fixtureRoute("x://a", "n")}, fixtureContext("n", "v"), "x://a/f", "x://a"),
	} {
		if other == base {
			t.Errorf("the identity ignores the %s", name)
		}
	}
	if identity("h1", []brokerpolicy.Route{fixtureRoute("x://a/", "n")}, fixtureContext("n", "v"), "x://a/f", "x://a") != base {
		t.Error("the identity is not deterministic")
	}
	if len(base) != 64 {
		t.Error("identity is not a fixed-width digest")
	}
}
