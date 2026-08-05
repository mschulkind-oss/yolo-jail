package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// describe prints the resolved confinement + packs + a description hash; --json is the
// canonical config, --hash is the (unsealed-marked) pin.
func TestDescribeVerb(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail","resources":{"pids_limit":4096}}`)

	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "confinement") || !strings.Contains(out.String(), "jail") {
		t.Errorf("describe should name the confinement notch:\n%s", out.String())
	}

	// --json is the canonical computed config.
	out.Reset()
	if rc := describeMain([]string{"--json"}, &out, &errw, false); rc != 0 {
		t.Fatalf("describe --json rc=%d", rc)
	}
	if !strings.Contains(out.String(), `"pids_limit": 4096`) {
		t.Errorf("describe --json must print the effective config:\n%s", out.String())
	}

	// --hash is a sha256, marked unsealed (not yet authoritative).
	out.Reset()
	if rc := describeMain([]string{"--hash"}, &out, &errw, false); rc != 0 {
		t.Fatalf("describe --hash rc=%d", rc)
	}
	if !strings.Contains(out.String(), "sha256:") || !strings.Contains(out.String(), "UNSEALED") {
		t.Errorf("describe --hash must print a sha256 marked unsealed:\n%s", out.String())
	}
}

// describeLine returns THE one output line whose prefix matches, failing if there is not
// exactly one. A report-wide strings.Contains is not good enough for the confinement vector:
// describe's other lines (and, in the wider suite, apply --host's kind census) mention most
// of these words somewhere, so a whole-output assertion passes even when the vector line is
// gone — which is exactly how a broken renderer looks green.
func describeLine(t *testing.T, out, prefix string) string {
	t.Helper()
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			hits = append(hits, l)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly 1 line starting %q, got %d:\n%s", prefix, len(hits), out)
	}
	return hits[0]
}

// describeVector returns the whole `enforced by` BLOCK — the label line plus its
// continuation lines, which is how a multi-primitive vector prints. Anchored on the single
// label line (describeLine's exactly-one rule) and then bounded by the continuation indent,
// so it can never silently widen to the rest of the report.
func describeVector(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	start := slices.Index(lines, describeLine(t, out, "  enforced by"))
	block := []string{lines[start]}
	for _, l := range lines[start+1:] {
		if !strings.HasPrefix(l, confinementLabelPad) {
			break
		}
		block = append(block, l)
	}
	return strings.Join(block, "\n")
}

// describeAt runs `describe` with a user-scope config naming a notch (`confinement` and
// `packs` are both user-scope keys) and returns its stdout.
func describeAt(t *testing.T, cfg string) string {
	t.Helper()
	home, _ := withHomeAndCwd(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	// Pin the mechanism so the printed vector is a property of the NOTCH here: unset, it
	// falls to a platform probe, and a darwin runner would resolve `container` and print the
	// VM where this asserts namespaces. The mechanism's own effect on the vector is the
	// table test below.
	t.Setenv("YOLO_RUNTIME", "podman")

	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d: %s", rc, errw.String())
	}
	return out.String()
}

// describe prints the resolved notch's PRIMITIVE VECTOR and its one policy bit — what
// actually enforces the confinement, and whether packs render their autonomous (prompts
// off) or guarded posture (plan §6c step 2). The load-bearing case is `host`: a preset that
// composes NOTHING must read as the weakest, not as just another name on the dial.
func TestDescribePrintsConfinementVector(t *testing.T) {
	// jail: the strongest preset — namespaces + a baked image, autonomy on.
	out := describeAt(t, `{"confinement":"jail"}`)
	enforced := describeVector(t, out)
	for _, want := range []string{"namespaces", "baked image"} {
		if !strings.Contains(enforced, want) {
			t.Errorf("jail vector should compose %q:\n%s", want, enforced)
		}
	}
	if strings.Contains(enforced, "nothing") {
		t.Errorf("jail must not print the empty vector:\n%s", enforced)
	}
	if a := describeLine(t, out, "  autonomy"); !strings.Contains(a, "ON") {
		t.Errorf("jail contains the agent, so autonomy is ON:\n%s", a)
	}

	// host: no primitive at all, and autonomy OFF — the §4.2 bit that keeps a pack's
	// jail-bypass keys off a real machine.
	out = describeAt(t, `{"confinement":"host"}`)
	if e := describeVector(t, out); !strings.Contains(e, "nothing") {
		t.Errorf("host composes no primitive and must say so:\n%s", e)
	}
	if a := describeLine(t, out, "  autonomy"); !strings.Contains(a, "OFF") {
		t.Errorf("host renders the guarded posture, so autonomy is OFF:\n%s", a)
	}
}

// The printed vector follows the MECHANISM, not just the notch name: `runtime` is what a
// launch will use, and a primitive belongs to the backend. Apple Container's jail is a VM
// where podman's is namespaces, and macos-user is the guest vector (separate user +
// Seatbelt) whatever the notch is called — printing "namespaces, baked image" for a backend
// that composes neither is the failure this output exists to remove.
func TestDescribeVectorFollowsMechanism(t *testing.T) {
	// A conf.notch/mechanism table rather than three CLI runs: confinementProfile is the
	// whole decision, and asserting it directly covers the darwin rows a Linux CI cannot run.
	// The notch is a render.Kind here, not the config string: describeMain resolves the name
	// once at the boundary (render.KindForNotch) and everything below reasons about the Kind.
	cases := []struct {
		notch     render.Kind
		mechanism string
		isMacOS   bool
		want      []render.Primitive
	}{
		{render.KindJail, "podman", false, []render.Primitive{render.PrimNamespaces, render.PrimBakedImage}},
		{render.KindJail, "container", true, []render.Primitive{render.PrimVM, render.PrimBakedImage}},
		{render.KindJail, "macos-user", true, []render.Primitive{render.PrimSeparateUser, render.PrimSeatbelt}},
		{render.KindGuest, "podman", false, []render.Primitive{render.PrimNamespaces, render.PrimLandlock}},
		{render.KindGuest, "container", true, []render.Primitive{render.PrimSeparateUser, render.PrimSeatbelt}},
		// host wins over any mechanism: the notch, not the backend, is what says "no boundary".
		{render.KindHost, "macos-user", true, nil},
	}
	for _, tc := range cases {
		prof := confinementProfile(tc.notch, tc.mechanism, tc.isMacOS)
		for _, prim := range tc.want {
			if !prof.Has(prim) {
				t.Errorf("%s/%s: vector should compose primitive %d", tc.notch, tc.mechanism, prim)
			}
		}
		// And nothing beyond it — a vector that over-claims is worse than one that is absent.
		for _, prim := range render.PrimitiveOrder() {
			if prof.Has(prim) && !slices.Contains(tc.want, prim) {
				t.Errorf("%s/%s: vector must not compose primitive %d", tc.notch, tc.mechanism, prim)
			}
		}
	}
}

// The "every primitive has a human phrasing" invariant moved to
// internal/render/confinement_test.go (TestEveryPrimitiveIsOrderedAndDescribed) along with the
// table itself (C2): the briefing header now renders the same vector for an agent, so the
// assertion belongs beside the one description both consumers read rather than in whichever
// caller happened to own it first.

// describe reports the RESOLVED `packages:` nix profile for a notch with no baked image
// (N2's fourth sub-item) — read from the GC-root symlink, never by invoking nix. The three
// states that matter: rooted (name the store path), declared-but-unresolved (say so), and
// a jail notch (say nothing, because a jail's packages come from the image).
func TestDescribeReportsPackageProfile(t *testing.T) {
	store := t.TempDir() // stands in for a /nix/store profile path
	root := filepath.Join(t.TempDir(), "package-roots", "packages")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store, root); err != nil {
		t.Fatal(err)
	}
	pkgs := []any{"ripgrep", "fd"}

	// host (no baked image), root present → names the resolved store path AND the root.
	var buf bytes.Buffer
	pr := richtext.Printer{W: &buf}
	printPackageProfile(pr, render.HostProfile(), pkgs, root)
	if !strings.Contains(buf.String(), store) {
		t.Errorf("a resolved profile must be named by its store path:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), root) {
		t.Errorf("the report should name the GC root that pins it:\n%s", buf.String())
	}

	// Root ABSENT → declared, not yet resolved. Must not print a bogus path.
	buf.Reset()
	printPackageProfile(richtext.Printer{W: &buf}, render.HostProfile(), pkgs,
		filepath.Join(t.TempDir(), "nope"))
	if !strings.Contains(buf.String(), "2 declared") {
		t.Errorf("an unresolved profile should still report the declared count:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "/nix/store") {
		t.Errorf("an unresolved profile must not name a store path:\n%s", buf.String())
	}

	// jail: a baked image supplies the packages, so there is NO profile to report. This is
	// the gate the mechanism's rename is about — printing a nix profile path for a jail
	// would name a closure the launch does not use.
	buf.Reset()
	printPackageProfile(richtext.Printer{W: &buf}, render.JailProfile(false), pkgs, root)
	if buf.Len() != 0 {
		t.Errorf("a jail's packages come from the image; nothing should be printed:\n%s", buf.String())
	}

	// No packages declared → nothing to report at any notch.
	buf.Reset()
	printPackageProfile(richtext.Printer{W: &buf}, render.HostProfile(), nil, root)
	if buf.Len() != 0 {
		t.Errorf("no declared packages => no line:\n%s", buf.String())
	}
}

// apply routes by notch; the not-yet-built notches fail closed (rc!=0) with an honest
// message rather than silently doing nothing, and a bogus --at is a usage error.
func TestApplyVerbRouting(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail"}`)

	var out, errw bytes.Buffer
	// jail: reports + describes, rc 0.
	if rc := applyMain(nil, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply (jail) rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "jail") {
		t.Errorf("apply (jail) should say so:\n%s", out.String())
	}

	// --host and --sealed are real now (Phases 4/5) with their own tests; their outcome
	// depends on packs/workspace, so this routing test does not assert them.

	// A bogus notch is a usage error (rc 2), not a silent default.
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--at", "bogus"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("apply --at bogus should be a usage error (rc 2), got %d", rc)
	}
}

// apply --sealed refuses when an UNDECLARED input is present (yolo-jail.local.jsonc)
// and passes when the workspace is clean of them. Runs in a scratch workspace so
// workspaceRoot() resolves there (not this repo's /workspace, which has real sidecars).
func TestApplySealedClosure(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	// A .yolo dir so workspaceRoot() anchors on this repo.
	writeFile(t, filepath.Join(repo, ".yolo", "keep"), "x")

	// Clean: no local.jsonc, no capture sidecars → sealed (rc 0).
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("clean workspace should seal (rc 0), got %d: %s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "sealed") {
		t.Errorf("clean seal should say 'sealed':\n%s", out.String())
	}

	// With yolo-jail.local.jsonc present → refused (rc 1), naming it.
	writeFile(t, filepath.Join(repo, "yolo-jail.local.jsonc"), `{"packages":["ripgrep"]}`)
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("local.jsonc present should refuse (rc 1), got %d", rc)
	}
	if !strings.Contains(out.String(), "yolo-jail.local.jsonc") || !strings.Contains(out.String(), "refused") {
		t.Errorf("refusal should name the undeclared input:\n%s", out.String())
	}
}

// apply --host renders config surfaces into a real home as PURE RMW (OQ-4): observe
// writes nothing, assert regenerates only the pack's managed keys and preserves the
// user's own, and non-config kinds are refused by name. Uses a scratch HOME.
func TestApplyHostRMW(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"hp","contributes":[
	  {"kind":"config","config":[{"agent":"hp","name":"settings","codec":"json","path":"~/.hp/settings.json","mode":"rmw","managed":{"telemetry":false}}]},
	  {"kind":"mount","host":"refs","into":"refs"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["file://`+packDir+`"],"confinement":"host"}`)

	// A pre-existing user key that RMW must preserve.
	writeFile(t, filepath.Join(home, ".hp", "settings.json"), `{"myOwnKey":"keep","telemetry":true}`)

	// Observe: writes nothing (the file keeps the user's telemetry:true).
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--host"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --host observe rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "refused") || !strings.Contains(out.String(), "mount") {
		t.Errorf("observe should refuse the mount kind by name:\n%s", out.String())
	}
	data, _ := os.ReadFile(filepath.Join(home, ".hp", "settings.json"))
	if !strings.Contains(string(data), `"telemetry": true`) && !strings.Contains(string(data), `"telemetry":true`) {
		t.Errorf("observe must not write — telemetry should still be the user's true:\n%s", data)
	}

	// Assert: managed key regenerated, user key preserved.
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--host", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --host --assert rc=%d: %s", rc, errw.String())
	}
	data, _ = os.ReadFile(filepath.Join(home, ".hp", "settings.json"))
	if !strings.Contains(string(data), "keep") {
		t.Errorf("RMW must preserve the user's own key:\n%s", data)
	}
	if !strings.Contains(string(data), "false") {
		t.Errorf("RMW must regenerate yolo's managed key to its declared value:\n%s", data)
	}
}
