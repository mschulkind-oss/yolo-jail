package loopholes

// jaildaemonpayload_test.go pins THE ONE COMPOSER of the YOLO_JAIL_DAEMONS payload against
// the argv it used to be welded into.
//
// The composition lived inside runtimeArgsFor — a container-argv builder — and was emitted
// only as `-e YOLO_JAIL_DAEMONS=`, which is a container flag. A backend with no container
// argv therefore never composed it at all: on macos-user a bare `"packs": ["claude"]` selects
// two jail daemons (openai-auth's adapter and wire-bridge's, both by unconditional `needs`)
// and started neither, silently — docs/design/jail-daemon-on-macos-user-plan.md's measured
// default. Set.JailDaemons is the seam that gives such a backend the same value to read.
//
// WHAT THIS FILE IS FOR IS THE EXTRACTION'S ONE RISK: that the argv moved. So the whole argv
// is pinned byte for byte (paths normalized), and the two spellings — compose-inside
// (RuntimeArgsForWithJailDaemons, what production used before the hoist and what
// internal/packload still calls) and compose-outside (JailDaemons + RuntimeArgsWithJailDaemons,
// what the run pipeline uses now) — are asserted EQUAL. Neither assertion alone is enough:
// the golden catches a change to both, the equality catches a change to either.
//
// Mutation checks (AGENTS.md: does it fail if I delete the call site?):
//   - delete the jailDaemonSpecs call from runtimeArgsFor → the golden loses its last two
//     entries and TestJailDaemonPayloadArgvIsByteIdentical fails.
//   - stop applying the origin gate in jailDaemonSpecs → TestJailDaemonsHonorsTheOriginGate
//     fails (it is the only assertion about the composer's gate; the argv-side gate is
//     convergence_test.go's).
//   - drop the `extra` append → the service half vanishes from both halves of the golden.

import (
	"strings"
	"testing"

	"path/filepath"
)

// payloadFixture stages two jail-daemon loopholes plus one that declares none, and returns
// the approving Set and the module root (for normalizing the golden).
//
// A THIRD MODULE WITH NO JAIL DAEMON is not padding: it is what proves the composer selects
// rather than merely counts — a composer that took every record would put `plain` in the
// payload and the supervisor would try to run a loophole with no argv.
func payloadFixture(t *testing.T) (Set, string) {
	t.Helper()
	unsetJail(t)
	md := modsDir(t)

	alpha := mkdir(t, filepath.Join(md, "alpha"))
	writeManifest(t, alpha, map[string]any{
		"name": "alpha", "description": "alpha",
		"jail_daemon": map[string]any{
			"cmd": []any{"yolo-jaild", "alpha-adapter"}, "restart": "always",
		},
	})
	beta := mkdir(t, filepath.Join(md, "beta"))
	writeManifest(t, beta, map[string]any{
		"name": "beta", "description": "beta",
		"jail_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/bin/beta"}},
	})
	plain := mkdir(t, filepath.Join(md, "plain"))
	writeManifest(t, plain, map[string]any{"name": "plain", "description": "plain"})

	return approvedSetFrom(md), md
}

// serviceExtras is the pack-service half of the payload, shaped the way
// run.serviceJailDaemons shapes it (sorted by name, restart defaulted).
func serviceExtras() []JailDaemonSpec {
	return []JailDaemonSpec{
		{Name: "wire-bridge", Cmd: []string{"yolo-jaild", "wire-bridge"}, Restart: "on-failure"},
	}
}

// normalizeRoot replaces the fixture's temp module root so the golden below is a fact about
// the ARGV rather than about t.TempDir().
func normalizeRoot(args []string, root string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, root, "<mods>")
	}
	return out
}

// TestJailDaemonPayloadArgvIsByteIdentical is the golden: the exact argv a launch with two
// jail-daemon loopholes and one pack service produces, through BOTH spellings.
func TestJailDaemonPayloadArgvIsByteIdentical(t *testing.T) {
	set, root := payloadFixture(t)
	enabled := set.Enabled()

	want := []string{
		"-v", "<mods>/alpha:/etc/yolo-jail/loopholes/alpha:ro",
		"-v", "<mods>/beta:/etc/yolo-jail/loopholes/beta:ro",
		"-e", `YOLO_JAIL_DAEMONS=[{"name": "alpha", "cmd": ["yolo-jaild", "alpha-adapter"], ` +
			`"restart": "always"}, {"name": "beta", ` +
			`"cmd": ["/etc/yolo-jail/loopholes/beta/bin/beta"], "restart": "on-failure"}, ` +
			`{"name": "wire-bridge", "cmd": ["yolo-jaild", "wire-bridge"], "restart": "on-failure"}]`,
	}

	inside := normalizeRoot(set.RuntimeArgsForWithJailDaemons(enabled, "", serviceExtras()), root)
	if strings.Join(inside, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the compose-inside argv moved.\n got: %q\nwant: %q", inside, want)
	}

	// The run pipeline's spelling: the payload composed ABOVE the backend dispatch and
	// handed back to be serialized. Same bytes, or the hoist changed what a container gets.
	outside := normalizeRoot(
		set.RuntimeArgsWithJailDaemons(enabled, "", set.JailDaemons(enabled, "", serviceExtras())),
		root)
	if strings.Join(outside, "\x00") != strings.Join(inside, "\x00") {
		t.Errorf("composing the payload above the dispatch changes the container argv — the "+
			"hoist is supposed to be invisible to a container launch.\n outside: %q\n inside:  %q",
			outside, inside)
	}
}

// The composer SELECTS: two of the three fixture loopholes declare a daemon, the service
// extras come last and in the order given, and `{jail_loophole_dir}` is already resolved
// (load-time substitution, not the composer's business).
func TestJailDaemonsComposesDeclaredDaemonsThenExtras(t *testing.T) {
	set, _ := payloadFixture(t)
	specs := set.JailDaemons(set.Enabled(), "", serviceExtras())

	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "alpha,beta,wire-bridge" {
		t.Fatalf("payload names = %v, want the two declaring loopholes then the service", names)
	}
	if got := specs[1].Cmd[0]; got != "/etc/yolo-jail/loopholes/beta/bin/beta" {
		t.Errorf("beta's argv[0] = %q — {jail_loophole_dir} is substituted at LOAD time and the "+
			"composer must carry the cmd verbatim", got)
	}
	if got := specs[1].Restart; got != "on-failure" {
		t.Errorf("beta declared no restart and its spec says %q; the default is loopholedecl's, "+
			"applied at load, and the composer must not re-invent one", got)
	}
}

// A payload with nothing in it emits NO env var at all — a bare `yolo -- bash` must not grow
// YOLO_JAIL_DAEMONS merely because the composition ran.
func TestEmptyJailDaemonPayloadEmitsNoEnvVar(t *testing.T) {
	set, _ := payloadFixture(t)
	args := set.RuntimeArgsWithJailDaemons(set.Enabled(), "", nil)
	for _, a := range args {
		if strings.HasPrefix(a, "YOLO_JAIL_DAEMONS=") {
			t.Fatalf("an empty payload still emitted the variable: %q", args)
		}
	}
}

// THE ORIGIN GATE, at the composer's own face (the design's G3, which never graduated —
// `git show 9190a4d1^:docs/design/loophole-packaging.md`, §4.3). A pack-shipped loophole
// whose pack is unapproved contributes NOTHING to the payload — the same withholding
// RuntimeArgsFor applies to its binds, devices and CA. Without this the hoist would be a
// hole in the gate: the payload names an argv the jail would run.
func TestJailDaemonsHonorsTheOriginGate(t *testing.T) {
	set, _ := payloadFixture(t)
	// Same records, no gate entries: LoadLoophole's fail-safe label is SourcePack, so
	// MayRunHostCode is false for every one of them.
	ungated := Set{all: set.All()}
	if specs := ungated.JailDaemons(ungated.Enabled(), "", nil); len(specs) != 0 {
		t.Errorf("an unapproved pack's jail daemon reached the payload: %+v", specs)
	}
	// The extras still cross: a pack SERVICE crosses no host boundary, so there is nothing
	// for an origin gate to withhold (kinds.go's anti-loophole).
	if specs := ungated.JailDaemons(ungated.Enabled(), "", serviceExtras()); len(specs) != 1 {
		t.Errorf("the gate dropped a pack SERVICE's daemon, which crosses no boundary: %+v", specs)
	}
}

// Apple Container's `intercepts` skip drops a loophole WHOLE, its jail daemon included — the
// mounts loop and the composer share one predicate (admitsJailSideEffects), so a payload can
// never name a daemon whose module mount was left off the argv.
func TestAppleContainerInterceptSkipDropsTheJailDaemonToo(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "intercepting"))
	writeManifest(t, mod, map[string]any{
		"name": "intercepting", "description": "x",
		"intercepts":  []any{map[string]any{"host": "example.test"}},
		"broker_ip":   "127.0.0.1",
		"jail_daemon": map[string]any{"cmd": []any{"yolo-jaild", "terminator"}},
	})
	set := approvedSetFrom(md)

	if specs := set.JailDaemons(set.Enabled(), "podman", nil); len(specs) != 1 {
		t.Fatalf("podman must carry the daemon: %+v", specs)
	}
	if specs := set.JailDaemons(set.Enabled(), "container", nil); len(specs) != 0 {
		t.Errorf("Apple Container skips an intercepting loophole whole (apple/container#673), "+
			"so its daemon must not be in the payload either: %+v", specs)
	}
	for _, a := range set.RuntimeArgsWithJailDaemons(set.Enabled(), "container", nil) {
		if strings.Contains(a, "intercepting") {
			t.Errorf("...and no mount of it either: %q", a)
		}
	}
}
