package run

// briefingparity_test.go is the CROSS-NOTCH gate: one pack set must compose to the SAME BYTES at
// every briefing destination whether the jail composes it (run.packBriefingProses →
// jailcontent.ComposePackBriefings, per destination, filtering each file by audience) or the host
// does (packload.ResolveDestinations → entrypoint.ComposeHostBriefings, one section per pack per
// destination). The two notches reach the answer by different mechanisms, which is exactly how
// they drifted before (docs/reference/pack-system.md#briefing-r5); nothing compared them until this file.
//
// The fixture is chosen so each known way to diverge changes bytes:
//
//   - a pack whose briefing/ files and addressed files INTERLEAVE by filename, so the host must
//     sort per pack rather than append per contribution;
//   - an agent pack that ships its OWN briefing/ prose, so a host that skips the broadcasting
//     pack's own destinations (the old `other == p` in borrowedDestinations) loses it;
//   - a manifest-less pack after it, so pack order and one-blank-line spacing are both visible;
//   - provenance on AND off, so a label per file (instead of per contiguous run) shows.

import (
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestJailAndHostComposeTheSameBriefing(t *testing.T) {
	claude := jailPack(t, "claude", map[string]string{"briefing/claude-own.md": "Claude pack's own prose.\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Agent: "claude", Into: ".claude/CLAUDE.md"})
	pi := jailDest(t, "pi", ".pi/agent/AGENTS.md", "pi")
	matt := jailPack(t, "matt", map[string]string{
		"briefing/house-rules.md": "House rules.\n",
		"briefing/pi.md":          "Pi, from the convention dir.\n",
		"briefing/zz-late.md":     "Late house rule.\n\n",
		"files/pi-rules.md":       "Pi rules.\n",
		"AGENTS.md":               "Contributor guide — never shipped.\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "files/pi-rules.md", Agents: []string{"pi"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "briefing/pi.md", Agents: []string{"pi"}})
	zc := jailPack(t, "zc", map[string]string{"briefing/a.md": "Zero-ceremony prose.\n"})
	packs := []*packload.Pack{claude, pi, matt, zc}

	o := &Options{Stdout: discardBuf()}
	var proses []jailcontent.PackBriefing
	for _, p := range packs {
		proses = append(proses, o.packBriefingProses(p.Name, p)...)
	}
	home := t.TempDir()
	resolved, _ := packload.ResolveDestinations(packs)

	for _, provenance := range []bool{false, true} {
		host := map[string]string{}
		for _, d := range entrypoint.ComposeHostBriefings(resolved, home, provenance) {
			rel, err := filepath.Rel(home, d.Path)
			if err != nil {
				t.Fatal(err)
			}
			host[filepath.ToSlash(rel)] = d.Content
		}
		dests := briefingDestinations(packs)
		if len(dests) != 2 {
			t.Fatalf("destinations = %+v, want claude's and pi's", dests)
		}
		for _, d := range dests {
			jail := strings.TrimPrefix(
				jailcontent.ComposePackBriefings("", proses, d.Agent, provenance), "\n\n")
			if jail != host[d.Into] {
				t.Errorf("provenance=%v %s:\n--- jail ---\n%s--- host ---\n%s", provenance, d.Into,
					jail, host[d.Into])
			}
		}
		// And the composition is the RIGHT one, not merely the same one at both notches.
		want := map[string]string{
			".claude/CLAUDE.md": "Claude pack's own prose.\n\nHouse rules.\n\nLate house rule.\n\n" +
				"Zero-ceremony prose.\n",
			".pi/agent/AGENTS.md": "Claude pack's own prose.\n\nHouse rules.\n\n" +
				"Pi, from the convention dir.\n\nLate house rule.\n\nPi rules.\n\nZero-ceremony prose.\n",
		}
		if provenance {
			want = map[string]string{
				".claude/CLAUDE.md": "<!-- from pack: claude -->\nClaude pack's own prose.\n\n" +
					"<!-- from pack: matt -->\nHouse rules.\n\nLate house rule.\n\n" +
					"<!-- from pack: zc -->\nZero-ceremony prose.\n",
				".pi/agent/AGENTS.md": "<!-- from pack: claude -->\nClaude pack's own prose.\n\n" +
					"<!-- from pack: matt -->\nHouse rules.\n\nPi, from the convention dir.\n\n" +
					"Late house rule.\n\nPi rules.\n\n<!-- from pack: zc -->\nZero-ceremony prose.\n",
			}
		}
		for dest, body := range want {
			if host[dest] != body {
				t.Errorf("provenance=%v %s = %q\nwant %q", provenance, dest, host[dest], body)
			}
		}
	}
}

// THE SKILLS HALF of the cross-notch gate: one pack set must deliver the same skills SOURCES to
// every skills destination at both notches. The jail stages every pack's sources
// (packSkillSourceDirs) into every declared target (packSkillTargets), filtered by audience; the
// host borrows destinations per source (ResolveDestinations → hostskills.ComposeHostSkills). The
// fixture carries the two shapes where the host's per-source governance can drift from the jail:
//
//   - a pack whose skills/ sits beside an ADDRESSED narrower tree — the old pack-system.md#one-governance-reader gate dropped the
//     implicit skills/ as soon as any skills content contribution existed, at one notch only;
//   - an agent pack shipping its OWN skills/, which must reach its own destination (no
//     `other == p` self-skip) and every other agent's.
func TestJailAndHostDeliverTheSameSkills(t *testing.T) {
	const skill = "---\nname: x\n---\n"
	claude := jailPack(t, "claude", map[string]string{"skills/claude-own/SKILL.md": skill},
		packdecl.Contribution{Kind: packdecl.KindSkills, Agent: "claude", Into: ".claude/skills"})
	pi := jailPack(t, "pi", nil,
		packdecl.Contribution{Kind: packdecl.KindSkills, Agent: "pi", Into: ".pi/agent/skills"})
	s := jailPack(t, "s", map[string]string{
		"skills/one/SKILL.md":    skill,
		"pi-skills/two/SKILL.md": skill,
	}, packdecl.Contribution{Kind: packdecl.KindSkills, From: "pi-skills", Agents: []string{"pi"}})
	packs := []*packload.Pack{claude, pi, s}

	// rel names a source by pack and pack-relative dir, so the two notches' absolute paths
	// compare and a failure reads.
	roots := map[string]string{}
	for _, p := range packs {
		roots[p.Root] = p.Name
	}
	rel := func(dir string) string {
		for root, name := range roots {
			if r, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(r, "..") {
				return name + ":" + filepath.ToSlash(r)
			}
		}
		return dir
	}

	o := &Options{Stdout: discardBuf()}
	var sources []jailcontent.PackSkillSource
	for _, p := range packs {
		sources = append(sources, o.packSkillSourceDirs(p)...)
	}
	jail := map[string][]string{}
	for _, target := range packSkillTargets(packs) {
		for _, src := range sources {
			// The jail's audience filter (jailcontent.sourceAddressesAgent): empty broadcasts,
			// otherwise the destination's declared identity must be named.
			if len(src.Agents) == 0 || slices.Contains(src.Agents, target.Agent) {
				jail[target.Dest] = append(jail[target.Dest], rel(src.Dir))
			}
		}
		sort.Strings(jail[target.Dest])
	}

	home := t.TempDir()
	resolved, _ := packload.ResolveDestinations(packs)
	host := map[string][]string{}
	for _, d := range hostskills.ComposeHostSkills(resolved, home) {
		r, err := filepath.Rel(home, d.Dir)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.ToSlash(r)
		for _, l := range d.Layers {
			for _, src := range l.Sources {
				host[dest] = append(host[dest], rel(src))
			}
		}
		sort.Strings(host[dest])
	}

	want := map[string][]string{
		".claude/skills":   {"claude:skills", "s:skills"},
		".pi/agent/skills": {"claude:skills", "s:pi-skills", "s:skills"},
	}
	if !reflect.DeepEqual(jail, want) {
		t.Errorf("jail skills = %v\nwant %v", jail, want)
	}
	if !reflect.DeepEqual(host, jail) {
		t.Errorf("the notches diverge:\njail = %v\nhost = %v", jail, host)
	}
}
