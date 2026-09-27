package entrypoint

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// testPacksForAgent is the pack set a launch carries when its config selects agent (a pack
// name) and extra: that selection plus every pack the selection closure adds for it.
//
// The closure is the LAUNCH'S OWN resolver, packload.Selection.Close
// (internal/cli/run/packs.go calls it over the loaded packs), drawing from the embedded
// official set the way the launch does. Re-implementing the `needs` walk here would be a
// second answer to "which packs does this launch carry", so a manifest change could move
// the launch and leave the fixtures behind.
//
// It exists for docs/design/pi-codex-provider-shadowing.md P3 (R2): a derive invariant
// asserted over the agent pack alone passes vacuously when the provider it guards against
// arrives through a dependency. pi's `needs` joins openai-auth unconditionally, and
// openai-auth is the pack that declares openai-codex, so a pi fixture without it never
// hands the derive the row it must not write.
//
// No profile is selected at closure time, so the via half adds nothing. That is the launch's
// answer too for every profile these fixtures render: a via profile is the only one that
// adds a pack, and a test of one builds its own Selection.
func testPacksForAgent(t *testing.T, agent string, extra ...string) []*packload.Pack {
	t.Helper()
	all, err := embeddedPackSet()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*packload.Pack, len(all))
	for _, p := range all {
		byName[p.Name] = p
	}
	selected := make([]*packload.Pack, 0, 1+len(extra))
	for _, name := range append([]string{agent}, extra...) {
		p, ok := byName[name]
		if !ok {
			t.Fatalf("no embedded pack named %q", name)
		}
		selected = append(selected, p)
	}
	added, _, err := packload.Selection{
		Embedded: func(name string) (*packload.Pack, bool) {
			p, ok := byName[name]
			return p, ok
		},
	}.Close(selected)
	if err != nil {
		t.Fatalf("selection closure over %v: %v", append([]string{agent}, extra...), err)
	}
	return append(selected, added...)
}

func packNames(packs []*packload.Pack) []string {
	names := make([]string, 0, len(packs))
	for _, p := range packs {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

// The helper follows the shipped manifests' `needs`, an unconditional need and a
// conditional one both, so a fixture built from it moves when a manifest does.
func TestTestPacksForAgentResolvesTheNeedsClosure(t *testing.T) {
	cases := []struct {
		agent string
		extra []string
		want  []string
	}{
		// pi's unconditional need: openai-auth, the pack declaring openai-codex.
		{agent: "pi", want: []string{"openai-auth", "pi"}},
		// cerebras needs wire-bridge only when claude or copilot is installed; pi is neither.
		{agent: "pi", extra: []string{"cerebras"}, want: []string{"cerebras", "openai-auth", "pi"}},
		// claude's needs are unconditional, and they satisfy kilo's conditional one as well.
		{agent: "omp", extra: []string{"claude", "kilo"},
			want: []string{"aws-auth", "claude", "kilo", "omp", "openai-auth", "wire-bridge"}},
	}
	for _, c := range cases {
		if got := packNames(testPacksForAgent(t, c.agent, c.extra...)); !slices.Equal(got, c.want) {
			t.Errorf("testPacksForAgent(%s, %v) = %v, want %v", c.agent, c.extra, got, c.want)
		}
	}
}
