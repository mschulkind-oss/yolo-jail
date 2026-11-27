package callbroker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// H13: the route that wins is the bucket route even when the operation sits under a longer
// prefix a rule names; the identity records the authored bucket scope bytes, and no value.
func TestRoutedIdentityRecordsTheWinningBucketScope(t *testing.T) {
	rc, err := selectRoutedContext("fixture", "decl-hash", fixtureDeclaredContext(),
		fixtureRouting(t, []brokerpolicy.Route{fixtureRoute("x://a/", "n")}, fixtureContext("n", "private-value")),
		[]brokerpolicy.Operation{fixtureOp("x://a/team/f", "x://a/team/", "x://a")})
	if err != nil || rc == nil {
		t.Fatalf("%+v %v", rc, err)
	}
	want, err := brokerstore.ContextIdentity("fixture@decl-hash", "x://a/", "n", map[string]string{"account": "private-value"})
	if err != nil || rc.Digest != want {
		t.Fatalf("identity did not record the authored bucket scope: %s vs %s (%v)", rc.Digest, want, err)
	}
	if strings.Contains(rc.Digest, "private-value") || strings.Contains(rc.Name, "private-value") {
		t.Fatal("a field value leaked into the identity or name")
	}
}

// TestDeclaredExecutionContexts is the design's section 12.6 witness table (F1-F15) for the
// core half of routing. Each subtest names the scenario it proves; the config half (user-only
// positioned TOML, field-named load errors) is TestDeclaredExecutionContextsConfig and
// TestCurrentBrokerRouting* in internal/config. F9 (aws-auth and its full-check report)
// is proven by those files being untouched, which the unit's diff shows, and by their own
// retained tests.
func TestDeclaredExecutionContexts(t *testing.T) {
	// F1-F3 run one real selected handler: a routed read and write see the declared
	// variable and no static credential name; a call under no route is the captured env.
	for _, name := range []string{"F1", "F2", "F3"} {
		t.Run(name, routedHandlerRuns)
	}
	t.Run("F4", routedMostSpecificFirst)
	t.Run("F5", func(t *testing.T) {
		// The jail names no environment: it sends an op, argv and stdin, and a profile it
		// puts in argv is only argv. The route decides what the host program is given.
		f := routedFixture(t)
		routeAlpha(t, f, "finance-ro")
		if v, _, err := f.call(t, "act", "read", "--profile", "jail-chosen"); err != nil || v.Verdict != "runs" {
			t.Fatalf("%+v %v", v, err)
		}
		if got := f.env(t, "read")["SYNTHETIC_PROFILE"]; got != "finance-ro" {
			t.Fatalf("the route did not decide: %q", got)
		}
	})
	t.Run("F6", routeEditInvalidates)
	t.Run("F7", routedCallRefusals)
	t.Run("F8", func(t *testing.T) {
		// A workspace file declares a route and a context: it is not read for routing, so the
		// call is the default context. (The config load refuses the same file, in internal/config.)
		f := routedFixture(t)
		workspaceFile := "[contexts.finance]\nsource = \"local-tasks\"\nprofile = \"workspace-chosen\"\n[scopes.alpha]\nsource = \"local-tasks\"\nresource = \"team:alpha\"\ncontext = \"finance\"\n"
		if err := os.WriteFile(filepath.Join(f.h.workspace, "yolo-jail.toml"), []byte(workspaceFile), 0600); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "read"); err != nil || v.Verdict != "runs" {
			t.Fatalf("%+v %v", v, err)
		}
		env := f.env(t, "read")
		if _, routed := env["SYNTHETIC_PROFILE"]; routed || env["SYNTHETIC_STATIC_KEY"] != "static-key-value" {
			t.Fatalf("a workspace file routed a call: %v", env)
		}
	})
	t.Run("F10", undeclaredBlockIsDefault)
	t.Run("F11", routedIdentityBinds)
	t.Run("F12", func(t *testing.T) {
		// The classifier's input type has no field a context value could ride in, and the
		// stored records carry none (routedWriteBound scans the store and the audit log).
		fields := map[string]bool{}
		for i, typ := 0, reflect.TypeOf(CLIInput{}); i < typ.NumField(); i++ {
			fields[typ.Field(i).Name] = true
		}
		for _, name := range []string{"Version", "Front", "Argv", "StdinSHA256", "Roots", "Facts"} {
			delete(fields, name)
		}
		if len(fields) != 0 {
			t.Fatalf("the classifier's input gained fields %v: a context or environment value must never be among them", fields)
		}
		routedWriteBound(t)
	})
	t.Run("F13", routedEditsACopy)
	t.Run("F14", routedValuesVerbatim)
	t.Run("F15", routedRefusals)
	t.Run("redaction", routedValuesRedacted)
}
