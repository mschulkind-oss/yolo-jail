package luahook

// shippedpacks_test.go runs every derive.lua yolo SHIPS through the real VM.
//
// It is the tripwire docs/design/lua-transform-removal.md P1 names: the transform
// removal was a SPLIT of this package, not a deletion, and "derive.lua still runs
// byte-for-byte as it does today" is the condition the cut had to meet. Every other
// test here drives a FIXTURE script, and a fixture exercises the API subset its author
// happened to write — so a mis-cut that took something only the real packs reach leaves
// the whole suite green and breaks every jail at boot.
//
// The three registration spaces are all covered on purpose, because they are separate
// key spaces inside one script (derive.go): DeriveRegistrations reports the surface
// producers, Derive invokes each, and Env: true invokes the env producer — which the
// jail never calls (host-side only, packload/deriveenv.go), so nothing else here would
// notice it breaking.
//
// WHAT IT CATCHES, measured by mutation on 2026-09-12 rather than asserted. Removing
// Lua's `table` lib from openSandboxLibs fails this test at the `pi` subtest (its model
// loop calls table.insert); dropping `providers` from knownDeriveSources fails it at
// `claude` and `copilot`, through the Env leg. Removing `string` or `math` does NOT fail
// this test — no shipped derive calls them past its guard — and catching those is
// derivesandbox_test.go's TestDeriveSandbox_SafeLibsAvailable's job, not this one's. The
// two are complementary: that one pins the sandbox SURFACE, this one pins that the
// scripts yolo actually ships still run on it.
//
// It is NOT the only thing that notices either mutation, and was described that way
// until the claim was re-measured: both are also caught outside this package (the
// `providers` cut reddens provider-selection tests in cli, cli/run, entrypoint and
// packload; the `table` cut reddens TestGoToLuaIterationOrderIsDeterministic here and
// tests in entrypoint). What is uniquely this test's is the INPUT — every other test in
// the package drives a fixture script, so a mis-cut that breaks only a construct the real
// packs use, in a pack whose surfaces no other test renders, lands here first and alone.
//
// It reads the packs off disk by relative path rather than through packload, which
// imports this package.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEveryShippedPackDeriveStillRuns(t *testing.T) {
	paths, err := filepath.Glob("../../../packs/*/derive.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		// Not "no packs ship one" — this test cannot tell that from a moved tree, and
		// silently asserting nothing is the failure mode it exists to prevent.
		t.Fatal("found no packs/*/derive.lua: the glob is wrong, or the tree moved")
	}

	for _, p := range paths {
		pack := filepath.Base(filepath.Dir(p))
		t.Run(pack, func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			regs, err := (GopherLuaVM{}).DeriveRegistrations(string(src))
			if err != nil {
				t.Fatalf("DeriveRegistrations: %v", err)
			}
			if len(regs) == 0 {
				t.Errorf("%s registers no surface producer — a derive.lua that registers "+
					"nothing is a script whose top level stopped running", p)
			}
			for _, r := range regs {
				out, derr := (GopherLuaVM{}).Derive(string(src), shippedPackCtx(r.Agent, r.Surface, false))
				if derr != nil {
					t.Errorf("Derive(%s/%s): %v", r.Agent, r.Surface, derr)
					continue
				}
				t.Logf("surface %s/%s -> %d keys", r.Agent, r.Surface, len(out))
			}
			// The env producer is a THIRD key space, reached only with Env: true, and the
			// jail never invokes it — so a break here is invisible to every boot test.
			if _, eerr := (GopherLuaVM{}).Derive(string(src), shippedPackCtx(pack, "", true)); eerr != nil {
				t.Errorf("Derive(env): %v", eerr)
			}
		})
	}
}

// shippedPackCtx is the live-config world the shipped derives are run against. It is
// deliberately POPULATED rather than empty: every one of these scripts opens with a
// "nothing configured -> return {}" guard, so an empty ctx runs the guard and nothing
// else, and the test would pass with the whole body of every derive deleted.
//
// Each source below buys specific lines. mcp_servers reaches the MCP projections (agy,
// claude, codex, copilot, opencode). providers — with an endpoint, a wire_api and a
// models map — reaches pi's catalog loop and its `table.insert` over the model aliases,
// which is the only stdlib call any shipped derive makes past its guard. Measured by
// mutation: emptying providers takes pi/models from 1 key to 0 and this test stops
// noticing whether the model branch runs at all.
func shippedPackCtx(agent, surface string, env bool) *DeriveCtx {
	return &DeriveCtx{
		Agent:            agent,
		Surface:          surface,
		Env:              env,
		SelectedProvider: "probeprov",
		ProfileName:      "probe",
		Profile:          map[string]string{"model": "probe-model"},
		Tables: map[string]map[string]any{
			"mcp_servers": {"probe": map[string]any{"command": "/bin/true"}},
			"lsp_servers": {"probelsp": map[string]any{"command": "/bin/true"}},
			"providers": {"probeprov": map[string]any{
				"base_url":         "https://probe.example/v1",
				"wire_api":         "openai-chat-completions",
				"api_key_env_name": "PROBE_API_KEY",
				"models":           map[string]any{"fast": "probe-model-fast"},
			}},
		},
	}
}
