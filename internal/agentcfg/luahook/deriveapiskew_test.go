package luahook

// deriveapiskew_test.go pins the `yolo.*` API surface as a VERSION BOUNDARY:
// DeriveCtx.UnknownAPI's two modes, and the incident shape that made the boundary
// visible.
//
// THE INCIDENT. packs/claude/derive.lua grew a `yolo.env("claude", …)` call (f55f2109).
// Every jail whose baked image predated that commit executed the newly-staged script on a
// VM that had never registered `env`, and gopher-lua answered the only way it can:
//
//	surface claude/config: derive: lua transform error:
//	    <string>:51: attempt to call a non-function object
//
// Two properties of that failure are what these tests are really about. The message names
// neither the API nor the skew, so the remedy (rebuild the image) is unguessable from it.
// And the blast radius is the WHOLE SCRIPT, not the call: registration happens at load, so
// an unknown call at line 51 took down the producers registered at lines 4 and 26 — the
// claude/config and claude/settings surfaces both failed over an env producer the
// entrypoint never invokes at all.
//
// The call-site half — that a JAIL BOOTS with such a script staged, and says so on the way
// past — is pinned in entrypoint/deriveapiskew_test.go, the same split
// packload/skewkind_test.go and entrypoint/packskew_test.go already make for the manifest
// vocabulary. A test here alone would leave the tolerance switchable off with the suite
// green.

import (
	"strings"
	"testing"
)

// incidentShapedScript is the failure's exact geometry, minimized: a producer registered
// FIRST, a KNOWN registration beside it, and an unknown API called AFTER both. Anything
// that returns the layer proves the unknown call did not take the registrations down
// with it.
//
// The unknown member is a placeholder rather than `env`, and it has to be: `env` is known
// to THIS build, so a test written against the incident's literal text would pass on an
// unguarded VM. The placeholder is what `yolo.env` WAS to a pre-f55f2109 build, and is
// what the next API added will be to every image baked before it.
const incidentShapedScript = `
yolo.derive("claude", "config", function(ctx)
  return { mcpServers = ctx.mcp_servers }
end)

yolo.env("claude", function(ctx) return {} end)

yolo.api_from_a_newer_yolo("claude", function(ctx) return {} end)
`

// STRICT IS THE DEFAULT: a nil UnknownAPI keeps the authoring contract, where a name no
// build has is a mistake to hear about and not a degradation to accept — the same
// asymmetry packdecl.Decode/DecodeTolerant draws for the manifest vocabulary.
//
// What is pinned beyond "it still errors" is the MESSAGE, because the whole complaint
// about the old behavior was that its message said nothing. It must name the member, the
// APIs this build does have, and both readings of the situation.
func TestDeriveStrictRefusesAnUnknownAPIByName(t *testing.T) {
	_, err := GopherLuaVM{}.Derive(`yolo.frobnicate("claude")`,
		&DeriveCtx{Agent: "claude", Surface: "config"})
	if err == nil {
		t.Fatal("with no UnknownAPI callback the derive path is strict: an unknown " +
			"yolo.* member must be an error, so a pack author hears about a typo")
	}
	for _, want := range []string{
		"yolo.frobnicate", // the member, which "non-function object" never said
		"yolo.derive",     // what this build DOES have, read off the table
		"yolo.env",
		"version skew", // the reading a jail user needs
		"typo",         // the reading a pack author needs
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the strict refusal must name %q; got: %v", want, err)
		}
	}
}

// The strict refusal keeps gopher-lua's file:line, which is the only part of the old
// message that was any use. A raise from inside __index must be attributed to the SCRIPT
// line that read the member, not to the guard.
func TestDeriveStrictRefusalPointsAtTheOffendingLine(t *testing.T) {
	script := "\n\n\nyolo.frobnicate(\"claude\")\n"
	_, err := GopherLuaVM{}.Derive(script, &DeriveCtx{Agent: "claude", Surface: "config"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "<string>:4") {
		t.Errorf("the refusal must point at the line that read the unknown member "+
			"(<string>:4); got: %v", err)
	}
}

// THE FIX, at this layer: with a reporting callback the unknown call is a no-op and the
// rest of the script runs. The assertion is on the LAYER, not merely on err == nil —
// tolerance that swallowed the surface's producer along with the unknown call would be the
// same outage with a quieter message.
func TestDeriveTolerantSkipsAnUnknownAPIAndKeepsTheRestOfTheScript(t *testing.T) {
	var reported []string
	got, err := GopherLuaVM{}.Derive(incidentShapedScript, &DeriveCtx{
		Agent:      "claude",
		Surface:    "config",
		Tables:     map[string]map[string]any{"mcp_servers": {"fs": map[string]any{"command": "mcp-fs"}}},
		UnknownAPI: func(name string) { reported = append(reported, name) },
	})
	if err != nil {
		t.Fatalf("an unknown yolo.* member must not fail a tolerant derive — this is the "+
			"boot the incident cost: %v", err)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if servers["fs"] == nil {
		t.Errorf("the producer registered BEFORE the unknown call must still run and "+
			"produce its layer; got %#v", got)
	}
	if len(reported) != 1 || reported[0] != "api_from_a_newer_yolo" {
		t.Errorf("the skip must be reported once, naming the member; got %v", reported)
	}
}

// A tolerated skip nobody hears is the one outcome the skew rules forbid, so the callback
// IS the tolerance: there is no way to ask for one without the other. Pinned by asserting
// the report happens for a member the script only READS — a script may hold an unknown API
// in a local before calling it, and the report must not depend on the call.
func TestDeriveTolerantReportsAMemberThatIsOnlyRead(t *testing.T) {
	var reported []string
	_, err := GopherLuaVM{}.Derive(`local f = yolo.frobnicate`, &DeriveCtx{
		Agent:      "claude",
		Surface:    "config",
		UnknownAPI: func(name string) { reported = append(reported, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reported) != 1 || reported[0] != "frobnicate" {
		t.Errorf("reading an unknown member must report it; got %v", reported)
	}
}

// One unknown API is one finding, however many times the script touches it. Without this
// the guard would turn a loop into a wall of identical warnings, which is the repetition
// entrypoint's warnOnce exists to prevent — and the sink cannot dedupe what it is handed
// a thousand times per surface across five pack reads.
func TestDeriveTolerantReportsEachUnknownAPIOnce(t *testing.T) {
	script := `
for i = 1, 5 do yolo.frobnicate(i) end
yolo.frobnicate("again")
yolo.widget()
`
	var reported []string
	_, err := GopherLuaVM{}.Derive(script, &DeriveCtx{
		Agent:      "claude",
		Surface:    "config",
		UnknownAPI: func(name string) { reported = append(reported, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reported) != 2 {
		t.Fatalf("want one report per NAME (frobnicate, widget), got %v", reported)
	}
}

// The guard must not fire for an API this build does have, in either mode — a false
// positive here would report skew on every healthy boot and teach the reader to skim the
// one line that matters. The registered members are raw fields, so __index is never
// consulted for them; this is the test that fails if someone reimplements the guard as a
// wrapper that intercepts every read.
func TestDeriveGuardIsSilentForKnownAPIs(t *testing.T) {
	var reported []string
	got, err := GopherLuaVM{}.Derive(incidentShapedScript, &DeriveCtx{
		Agent:      "claude",
		Surface:    "config",
		UnknownAPI: func(name string) { reported = append(reported, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the registered producer must still be found and invoked")
	}
	for _, name := range reported {
		if name == "derive" || name == "env" {
			t.Errorf("yolo.%s is registered by this build and must never be reported "+
				"as unknown; reports were %v", name, reported)
		}
	}
	// And the env producer the script registered is still reachable through the env
	// spelling: tolerating unknown members must not disturb the known registration paths.
	_, envErr := GopherLuaVM{}.Derive(incidentShapedScript, &DeriveCtx{
		Agent: "claude", Env: true, UnknownAPI: func(string) {},
	})
	if envErr != nil {
		t.Fatalf("the yolo.env registration must still work: %v", envErr)
	}
}

// The guard is LOCKED, like ctx.managed's. A derive that could swap the metatable out
// could turn a reported skip back into the opaque failure the guard replaces — and, in
// strict mode, could turn a refusal into silence.
func TestDeriveGuardMetatableIsLocked(t *testing.T) {
	_, err := GopherLuaVM{}.Derive(`setmetatable(yolo, {})`, &DeriveCtx{
		Agent:      "claude",
		Surface:    "config",
		UnknownAPI: func(string) {},
	})
	if err == nil {
		t.Error("the yolo table's metatable must be locked: a script that can replace it " +
			"can disable the skew guard")
	}
}
