package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The never work must leave every config that has no `never` and no scoped line meaning what it
// meant: the gate's grants and request statuses for these nine configs are byte-identical to the
// golden, which was recorded from the tree before `never` was honored (the merge base's own
// harness cannot run them: main has no readable-gate fixture). A change that alters one of them is
// a change to legacy meaning, not to never.
func TestConfigsWithoutNeverKeepTheirLegacyGateOutput(t *testing.T) {
	remote := "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n"
	cases := map[string][2]string{
		"gh broad":               {"packs=[\"github\"]\n[allow.github]\nalways=[\"read\",\"create_pr\"]\non_request=[\"create_issue\"]\n", "[requests.github]\ncreate_pr=true\ncreate_issue=true\n"},
		"gh repo always":         {"packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow.github]\nalways=[\"create_pr\"]\non_request=[\"create_issue\"]\n", "[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.requests.github]\ncreate_pr=true\ncreate_issue=true\n"},
		"gh pattern":             {"packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n[repos.p]\nrepo=\"github.com/acme/*\"\n[repos.p.allow.github]\nalways=[\"create_pr\"]\n", "[repos.p]\nrepo=\"github.com/acme/*\"\n[repos.p.requests.github]\ncreate_pr=true\n"},
		"gh read only named":     {"packs=[\"github\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow.github]\nalways=[\"read\"]\n", "[repos.one]\nrepo=\"github.com/acme/app\"\n"},
		"git fetch":              {"packs=[\"git\"]\n[allow.git]\nalways=[\"fetch\"]\non_request=[\"push\"]\n", "[requests.git]\npush=[\"main\"]\n"},
		"git named":              {"packs=[\"git\"]\n[allow.git]\nalways=[\"fetch\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow]\nalways=[\"fetch\"]\n", "[repos.one]\nrepo=\"github.com/acme/app\"\n"},
		"git+gh":                 {"packs=[\"git\",\"github\"]\n[allow.git]\non_request=[\"fetch\",\"push\"]\n[allow.github]\nalways=[\"read\",\"create_pr\"]\n", "[requests.git]\npush=[\"main\"]\n[requests.github]\ncreate_pr=true\n"},
		"gh req outside ceiling": {"packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n", "[requests.github]\ncreate_pr=true\n"},
		"gh repo req no ceiling": {"packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n", "[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.requests.github]\ncreate_pr=true\n"},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	var got strings.Builder
	for _, n := range names {
		c := cases[n]
		t.Run(n, func(t *testing.T) {
			input, _ := scopedPolicyFixture(t, c[0], c[1])
			plan, err := ResolveReadableAllowance(input)
			if err != nil {
				fmt.Fprintf(&got, "CORPUS %s: LOAD ERR %v\n", n, err)
				return
			}
			ws := t.TempDir()
			if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte(remote), 0o644); err != nil {
				t.Fatal(err)
			}
			gate, err := ProduceReadableGateInput(ws, plan)
			if err != nil {
				fmt.Fprintf(&got, "CORPUS %s: GATE ERR %v\n", n, err)
				return
			}
			type sg struct {
				Source string
				Grants []string
			}
			var out []sg
			for _, s := range gate.Sources {
				var g []string
				for _, x := range s.Grants {
					g = append(g, x.String())
				}
				out = append(out, sg{s.Source, g})
			}
			b, _ := json.Marshal(out)
			rb, _ := json.Marshal(gate.Requests)
			fmt.Fprintf(&got, "CORPUS %s: %s REQ %s\n", n, b, rb)
		})
	}
	want, err := os.ReadFile(filepath.Join("testdata", "no_never_gate_corpus.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		t.Fatalf("legacy gate output changed for a config with no never:\n got: %s\nwant: %s", got.String(), want)
	}
}
