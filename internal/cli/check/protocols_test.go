package check

// protocols_test.go pins the PAIRING PREDICTION, and it drives `sectionPacks` rather than
// `protocolPairingGap` on purpose.
//
// A test that called the predictor directly would stay green with the call site deleted,
// which is the callee-pinned/call-site-unpinned shape AGENTS.md names and this repo has
// shipped six times. Every case below goes through the section, so removing the two lines
// in packs.go turns them red — verified by mutation, not assumed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// unpairablePack writes a local pack whose agent speaks one protocol and whose provider
// offers another, with no adapter anywhere: the launch refusal this section predicts.
//
// The two halves are ONE pack so the fixture needs no second entry and no `needs`
// resolution — the rule compares declarations, and whether they arrived from one manifest
// or two is not something the resolver can see.
func unpairablePack(t *testing.T, agentBin, agentProtocol, providerProtocol string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "unpairable",
  "contributes": [
    {"kind": "program", "bin": "` + agentBin + `", "via": "npm",
     "package": "@example/` + agentBin + `", "protocols": ["` + agentProtocol + `"]},
    {"kind": "provider", "name": "faraway",
     "endpoints": {"` + providerProtocol + `": {"base_url": "https://api.example.test/v1"}}},
    {"kind": "profile", "name": "faraway", "provider": "faraway"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// useProfiles builds the merged-config shape the section reads: `use_profiles` keyed by
// the agent's CLI name.
func useProfiles(agentBin, profile string) *jsonx.OrderedMap {
	inner := jsonx.NewOrderedMap()
	inner.Set(agentBin, profile)
	merged := jsonx.NewOrderedMap()
	merged.Set("use_profiles", inner)
	return merged
}

// A pairing nothing can serve FAILS the check, because the launch refuses it. A warning
// would be the defect this file closes with the sign flipped: `check` exiting 0 on a
// config that cannot start a jail.
func TestSectionPacksPredictsThePairingRefusal(t *testing.T) {
	pack := unpairablePack(t, "someagent", "anthropic", "openai")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("someagent", "faraway"))

	if r.failed == 0 {
		t.Fatalf("an unspeakable pairing must FAIL the check, not pass it:\n%s", buf.String())
	}
	out := buf.String()
	// The refusal has to name BOTH declarations, which is the whole discoverability
	// argument: a prediction that says only "this will be refused" sends the reader to
	// the launch to find out why.
	for _, want := range []string{"faraway", "someagent", "anthropic", "openai", "REFUSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("prediction should name %q:\n%s", want, out)
		}
	}
}

// The same pack with the protocols AGREEING must produce nothing at all — not a PASS line.
// It matches the launch, which never announces a gate it did not trip, and it keeps the
// golden that pins section ordering and the pass/warn/fail counts from moving.
func TestSectionPacksSaysNothingWhenThePairingResolves(t *testing.T) {
	pack := unpairablePack(t, "someagent", "openai", "openai")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("someagent", "faraway"))

	if r.failed != 0 {
		t.Errorf("a resolving pairing must not fail the check:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "REFUSED") {
		t.Errorf("a resolving pairing must produce no pairing line at all:\n%s", buf.String())
	}
}

// NO SELECTION, NO PAIRING. The gate is reached through a profile (§4.1), so a provider
// merely present in the table repoints nothing and must not be graded. This is the case a
// census-shaped predictor would get wrong — it would refuse a config the launch runs.
func TestSectionPacksIgnoresAnUnselectedProvider(t *testing.T) {
	pack := unpairablePack(t, "someagent", "anthropic", "openai")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())

	if r.failed != 0 {
		t.Errorf("an unselected provider must not be graded:\n%s", buf.String())
	}
}

// An agent nothing installs has no owner, so there is no `protocols` list to compare and
// nothing to refuse. It is the shape a user gets from a `use_profiles` key naming a CLI no
// selected pack ships, and the launch passes it through to the same silence.
func TestSectionPacksIgnoresAProfileForAnUninstalledAgent(t *testing.T) {
	pack := unpairablePack(t, "someagent", "anthropic", "openai")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("nobodysagent", "faraway"))

	if r.failed != 0 {
		t.Errorf("a profile for an agent no pack installs must not be graded:\n%s", buf.String())
	}
}
