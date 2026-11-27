package callbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// These tests drive the real selected handler: its declaration is a TOML one that carries a
// context block (segment scopes under `team:`), its routes and contexts are the host user
// config the handler rereads on every call, and its host program is a script that records
// the environment it was given. The source is a synthetic `local-tasks`, not AWS.

const routedFixtureTOML = `name = "tasks"
access = "read fixture tasks"
transport = "loopback-tls"

[[grants]]
kind = "read"
text = "read tasks"

[call_broker]
source = "local-tasks"
front = "cli"
program = "tasks"
classifier = ["{program:classifier}"]
env_from_host = ["SYNTHETIC_HOST_TOKEN", "SYNTHETIC_STATIC_KEY", "SYNTHETIC_PROFILE_HOST"]

[call_broker.scope]
roots = "declared"
nesting = "segment"
separator = ":"
anchors = ["team:"]

[call_broker.context]
unset = ["SYNTHETIC_STATIC_KEY"]
route_anchors = ["team:"]

[call_broker.context.fields]
profile = "SYNTHETIC_PROFILE"

[[call_broker.resource]]
type = "project"

[[call_broker.resource]]
type = "task"
parent = "project"

[[call_broker.resource]]
type = "pr"
parent = "project"

[[call_broker.cell]]
name = "change"
tables = ["working_set", "all"]

[[call_broker.cell]]
name = "blocked"
tables = ["all"]

[[call_broker.lookup]]
name = "task-project"
cli = ["lookup", "{id}"]
`

// routedProgram records the environment of every command under env-<command>, writes the
// marker for a write, and prints the profile variable it was given.
func routedProgram(home, marker string) string {
	return "#!/bin/sh\ncase \"$1\" in\n lookup) printf '{\"project\":\"team:alpha\"}';;\n" +
		" update-pr|update|read|read-beta) env | sort > '" + home + "/env-'\"$1\"; cat >/dev/null\n" +
		"  case \"$1\" in update*) printf 'WRITE\\n' >> '" + marker + "';; esac\n  printf 'profile=%s\\n' \"$SYNTHETIC_PROFILE\";;\n" +
		" *) exit 13;;\nesac\n"
}

func routedFixture(t *testing.T) *onceFixture {
	t.Helper()
	t.Setenv("SYNTHETIC_STATIC_KEY", "static-key-value")
	t.Setenv("SYNTHETIC_PROFILE_HOST", "host-profile")
	onceFixtureTOML, onceFixtureProgram = routedFixtureTOML, routedProgram
	t.Cleanup(func() { onceFixtureTOML, onceFixtureProgram = "", nil })
	f := selectedOnceFixture(t)
	f.store.Entries = RequestEntries
	if rc, err := f.h.routedContextFor(Classification{}); rc != nil || err != nil {
		t.Fatalf("no route is configured yet: %+v %v", rc, err)
	}
	return f
}

// routeAlpha writes the user config's context and route for team:alpha, as the editing
// verbs would: this is host user config, which the handler rereads on each call.
func routeAlpha(t *testing.T, f *onceFixture, profile string) {
	t.Helper()
	base := string(mustRead(t, f.userPath))
	if i := strings.Index(base, "\n[contexts."); i >= 0 {
		base = base[:i+1]
	}
	body := base + "[contexts.finance]\nsource = \"local-tasks\"\nprofile = \"" + profile + "\"\n[scopes.alpha]\nsource = \"local-tasks\"\nresource = \"team:alpha\"\ncontext = \"finance\"\n"
	if err := os.WriteFile(f.userPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *onceFixture) env(t *testing.T, command string) map[string]string {
	t.Helper()
	return recordedEnv(t, filepath.Join(f.home, "env-"+command))
}

// F1, F2, F3, F12 on the real handler: a read and a write under the route run in the
// context; a call under no route is byte for byte the captured environment.
func routedHandlerRuns(t *testing.T) {
	f := routedFixture(t)
	routeAlpha(t, f, "finance-ro")

	v, result, err := f.call(t, "act", "read")
	if err != nil || v.Verdict != "runs" {
		t.Fatalf("routed read: %+v %v", v, err)
	}
	routed := f.env(t, "read")
	if routed["SYNTHETIC_PROFILE"] != "finance-ro" {
		t.Fatalf("the routed read did not see the declared variable: %v", routed)
	}
	if _, present := routed["SYNTHETIC_STATIC_KEY"]; present {
		t.Fatal("a static credential name survived the unset on a routed call")
	}
	if routed["SYNTHETIC_PROFILE_HOST"] != "host-profile" || routed["SYNTHETIC_HOST_TOKEN"] != "synthetic-only-host-value" {
		t.Fatalf("other captured names must pass through: %v", routed)
	}
	if strings.Contains(string(result.Stdout), "finance-ro") {
		t.Fatalf("context value reached the jail unredacted: %q", result.Stdout)
	}

	// F3: a read under no route runs in the captured environment, static name present.
	if v, _, err := f.call(t, "act", "read-beta"); err != nil || v.Verdict != "runs" {
		t.Fatalf("default read: %+v %v", v, err)
	}
	plain := f.env(t, "read-beta")
	if plain["SYNTHETIC_STATIC_KEY"] != "static-key-value" || plain["SYNTHETIC_PROFILE_HOST"] != "host-profile" {
		t.Fatalf("the default context changed: %v", plain)
	}
	if _, present := plain["SYNTHETIC_PROFILE"]; present {
		t.Fatal("the default context received a routed variable")
	}
}

// An asking write under a route is filed under the context's name and identity, answered,
// spent once, and runs in the context; no field value is in any record (F12, 7.4).
func routedWriteBound(t *testing.T) {
	f := routedFixture(t)
	routeAlpha(t, f, "finance-ro")
	if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", v, err)
	}
	pending := f.pending(t)
	if len(pending) != 1 || pending[0].ContextName != "finance" || len(pending[0].ContextDigest) != 64 {
		t.Fatalf("the request is not bound to its context: %+v", pending)
	}
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	v, result, err := f.call(t, "act", "update-pr", "1")
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("answered write: %+v %v effects=%d", v, err, f.effects(t))
	}
	if env := f.env(t, "update-pr"); env["SYNTHETIC_PROFILE"] != "finance-ro" || env["SYNTHETIC_STATIC_KEY"] != "" {
		t.Fatalf("the write did not run in its context: %v", env)
	}
	if strings.Contains(string(result.Stdout), "finance-ro") {
		t.Fatalf("value in output: %q", result.Stdout)
	}
	entries, err := os.ReadDir(f.store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(string(mustRead(t, filepath.Join(f.store.Dir, entry.Name()))), "finance-ro") {
			t.Fatalf("a context value is in the store record %s", entry.Name())
		}
	}
	if strings.Contains(string(mustRead(t, filepath.Join(f.home, "audit.jsonl"))), "finance-ro") {
		t.Fatal("a context value is in the audit log")
	}
}

// F6: an edit to the route or the context while a request is pending, answered or
// covered by a timed entry makes the identity differ. The request fails revalidation, the
// answer is not spent, the entry does not cover, and the next call asks.
func routeEditInvalidates(t *testing.T) {
	t.Run("pending request", func(t *testing.T) {
		f := routedFixture(t)
		routeAlpha(t, f, "finance-ro")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		pending := f.pending(t)[0]
		if _, err := f.h.revalidate(pending); err != nil {
			t.Fatalf("unedited request must revalidate: %v", err)
		}
		routeAlpha(t, f, "finance-rw")
		if _, err := f.h.revalidate(pending); err == nil || !strings.Contains(err.Error(), "route or context changed") {
			t.Fatalf("an edited context revalidated: %v", err)
		}
	})
	t.Run("answered once", func(t *testing.T) {
		f := routedFixture(t)
		routeAlpha(t, f, "finance-ro")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		routeAlpha(t, f, "finance-rw")
		v, _, err := f.call(t, "act", "update-pr", "1")
		if v.Verdict == "runs" || f.effects(t) != 0 {
			t.Fatalf("an answer given for another context ran: %+v %v", v, err)
		}
		snap, err := f.store.Snapshot()
		if err != nil || len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
			t.Fatalf("the answer was spent: %+v %v", snap.Grants, err)
		}
	})
	t.Run("timed entry", func(t *testing.T) {
		f := routedFixture(t)
		routeAlpha(t, f, "finance-ro")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		if _, err := f.store.DecideWorking(f.pending(t)[0].ID, brokerstore.Decision{For: 10 * time.Minute}); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "runs" {
			t.Fatalf("the timed entry did not cover its own context: %+v %v", v, err)
		}
		routeAlpha(t, f, "finance-rw")
		before := f.effects(t)
		if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "asks" || f.effects(t) != before {
			t.Fatalf("an edited context was covered by the old entry: %+v %v", v, err)
		}
	})
	t.Run("route removed", func(t *testing.T) {
		// The default context neither extends nor satisfies a routed entry, nor the reverse.
		f := routedFixture(t)
		routeAlpha(t, f, "finance-ro")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		if _, err := f.store.DecideWorking(f.pending(t)[0].ID, brokerstore.Decision{For: 10 * time.Minute}); err != nil {
			t.Fatal(err)
		}
		base := string(mustRead(t, f.userPath))
		if err := os.WriteFile(f.userPath, []byte(base[:strings.Index(base, "[contexts.")]), 0600); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "asks" || f.effects(t) != 0 {
			t.Fatalf("a routed timed entry covered a default-context call: %+v %v", v, err)
		}
	})
}

// 7.2: operations that select different contexts refuse; a matched route whose context
// is broken fails and never runs in the default context; nothing runs.
func routedCallRefusals(t *testing.T) {
	t.Run("conflicting contexts", func(t *testing.T) {
		f := routedFixture(t)
		base := string(mustRead(t, f.userPath))
		body := base + "[contexts.one]\nsource = \"local-tasks\"\nprofile = \"p1\"\n[contexts.two]\nsource = \"local-tasks\"\nprofile = \"p2\"\n" +
			"[scopes.alpha]\nsource = \"local-tasks\"\nresource = \"team:alpha\"\ncontext = \"one\"\n[scopes.beta]\nsource = \"local-tasks\"\nresource = \"team:beta\"\ncontext = \"two\"\n"
		if err := os.WriteFile(f.userPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		v, _, err := f.call(t, "act", "update", "a1", "b1")
		if err != nil || v.Verdict != "refused" || !strings.Contains(v.Next, "different execution contexts") || f.effects(t) != 0 || len(f.pending(t)) != 0 {
			t.Fatalf("conflicting contexts: %+v %v", v, err)
		}
	})
	t.Run("F7 broken matched context", func(t *testing.T) {
		f := routedFixture(t)
		base := string(mustRead(t, f.userPath))
		body := base + "[scopes.alpha]\nsource = \"local-tasks\"\nresource = \"team:alpha\"\ncontext = \"absent\"\n"
		if err := os.WriteFile(f.userPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"read", "update-pr"} {
			v, _, err := f.call(t, "act", command, "1")
			if err != nil || v.Verdict != "refused" || !strings.Contains(v.Next, "default context") {
				t.Fatalf("%s: %+v %v", command, v, err)
			}
		}
		if _, err := os.Stat(filepath.Join(f.home, "env-read")); err == nil || f.effects(t) != 0 || len(f.pending(t)) != 0 {
			t.Fatal("a broken matched route ran something in the default context")
		}
	})
}
