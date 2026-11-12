package configmigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The oracle reads the converted files with the launch's own readers and compares them with what
// the same workspace asked before the migration, read by the same readers on the old files. It
// never calls the migration's helpers (lowerLegacyRepos, convert, merge): a grant is compared only
// after config.LegacyWorkspaceBrokerGrantsOf read the old TOML, and after Apply has written the new
// one.

// oracleCase is one workspace: its git remotes in order (the first is origin) and its old files.
type oracleCase struct {
	name    string
	remotes []string
	files   map[string]string
	// oldWrites are the write grants the pre-binding gate (f7b06d6a5, internal/config
	// NewScopeCheck on the same files, observed by a witness test there) granted: the old
	// consumer's actual output, pinned, never the migration's or the new gate's own projection.
	oldWrites []string
}

const oracleProject = "yolo-jail.toml"
const oracleLocal = "yolo-jail.local.toml"

// grantKeys is the set of every effective grant a read returns, each field spelled out, so a grant
// that differs in any field (kind, repository, branch, tag, workflow, actor, or repository pattern)
// is a different member.
func grantKeys(t *testing.T, got []config.WorkspaceBrokerGrants) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, w := range got {
		for _, g := range w.Grants {
			out[oracleGrantKey(w.Source, g)] = true
		}
	}
	return out
}

func oracleGrantKey(source string, g brokerscope.Grant) string {
	return fmt.Sprintf("%s|kind=%s|repo=%s|branch=%s|tag=%s|workflow=%s|actor=%s|pattern=%t",
		source, g.Kind, g.Repo, g.Branch, g.Tag, g.Workflow, g.Actor, g.RepoPattern)
}

func oracleSortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func diffGrantSets(want, got map[string]bool) (missing, extra []string) {
	for _, k := range oracleSortedKeys(want) {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for _, k := range oracleSortedKeys(got) {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	return missing, extra
}

func unplacedOf(got []config.WorkspaceBrokerGrants) []string {
	var out []string
	for _, w := range got {
		for _, u := range w.Unplaced {
			out = append(out, w.Source+": "+u)
		}
	}
	return out
}

func TestRealReadersSeeTheSameGrantsAfterTheTOMLReposMigration(t *testing.T) {
	cases := []oracleCase{
		{
			// The project asks for runs and approvals on origin; the local file lists one more repo.
			// Both layers' lists reach the same effective grants after the conversion.
			name:    "project asks, local file lists an extra repo",
			remotes: []string{"https://github.com/acme/app.git"},
			files: map[string]string{
				oracleProject: "[requests.github]\nruns = [\"nightly.yml\", \"release.yml\"]\napprove_runs = [\"github-actions[bot]\"]\ncreate_pr = true\n",
				oracleLocal:   "[requests.github]\nrepos = [\"other/lib\"]\n",
			},
			oldWrites: []string{"runs github.com/acme/app nightly.yml", "runs github.com/acme/app release.yml", "runs github.com/other/lib nightly.yml", "runs github.com/other/lib release.yml", "approve-run github.com/acme/app github-actions[bot]", "approve-run github.com/other/lib github-actions[bot]", "create-pr github.com/acme/app", "create-pr github.com/other/lib"},
		},
		{
			// A list in the project file and one in the local file are unioned, each repository once,
			// and a repo the origin already has is not granted twice.
			name:    "project and local lists union and dedup against origin",
			remotes: []string{"https://github.com/acme/app.git"},
			files: map[string]string{
				oracleProject: "[requests.github]\nrepos = [\"other/lib\", \"acme/app\"]\nruns = [\"nightly.yml\"]\n",
				oracleLocal:   "[requests.github]\nrepos = [\"other/lib\", \"third/x\"]\nruns = [\"nightly.yml\"]\n",
			},
			oldWrites: []string{"runs github.com/acme/app nightly.yml", "runs github.com/other/lib nightly.yml", "runs github.com/third/x nightly.yml"},
		},
		{
			// Before GW-D79 an unqualified line reached every remote: the other remote's table keeps it.
			name:    "two remotes: unqualified lines reach each remote",
			remotes: []string{"https://github.com/acme/app.git", "https://github.com/acme/up.git"},
			files: map[string]string{
				oracleProject: "[requests.github]\nruns = [\"nightly.yml\"]\n",
				oracleLocal:   "[requests.github]\nrepos = [\"other/lib\"]\n",
			},
			oldWrites: []string{"runs github.com/acme/app nightly.yml", "runs github.com/acme/up nightly.yml", "runs github.com/other/lib nightly.yml"},
		},
		{
			// A list in the local file only: the project file has no old key and keeps its bytes.
			name:    "only the local file lists repos",
			remotes: []string{"https://github.com/acme/app.git"},
			files: map[string]string{
				oracleProject: "# keep\n[requests.github]\nruns = [\"nightly.yml\"]\n",
				oracleLocal:   "[requests.github]\nrepos = [\"other/lib\"]\napprove_runs = [\"github-actions[bot]\"]\n",
			},
			oldWrites: []string{"runs github.com/acme/app nightly.yml", "runs github.com/other/lib nightly.yml", "approve-run github.com/acme/app github-actions[bot]", "approve-run github.com/other/lib github-actions[bot]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
			ws := realDir(t)
			namedRemotes(t, ws, remotePairs(tc.remotes)...)
			old := map[string][]byte{}
			for name, text := range tc.files {
				write(t, filepath.Join(ws, name), text)
				old[name] = []byte(text)
			}
			decls, _, err := config.ReadableDeclarationsForPacks(packload.Embedded())
			if err != nil {
				t.Fatalf("shipped declarations: %v", err)
			}

			// BEFORE: the old files as the legacy reader reads them.
			before, err := config.ReadWorkspaceForGate(ws)
			if err != nil {
				t.Fatalf("the old files do not read through the gate: %v", err)
			}
			wantRaw := config.LegacyWorkspaceBrokerGrantsOf(before.Config, ws, decls)
			if u := unplacedOf(wantRaw); len(u) > 0 {
				t.Fatalf("the old files leave grants unplaced before the migration: %v", u)
			}
			want := grantKeys(t, wantRaw)
			if len(want) == 0 {
				t.Fatalf("the old files grant nothing, so the oracle proves nothing")
			}

			// THE MIGRATION.
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
			if !p.Equal || len(p.NotConverted) > 0 {
				t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			// AFTER: the converted files through the same readers.
			after, err := config.ReadWorkspaceForGate(ws)
			if err != nil {
				t.Fatalf("the converted files do not read through the gate: %v", err)
			}
			gotRaw := config.WorkspaceBrokerGrantsOf(after.Config, ws, decls)
			if u := unplacedOf(gotRaw); len(u) > 0 {
				t.Fatalf("the converted files leave grants unplaced: %v", u)
			}
			missing, extra := diffGrantSets(want, grantKeys(t, gotRaw))
			if len(missing) > 0 || len(extra) > 0 {
				t.Fatalf("effective grants changed by the migration\nmissing after: %v\nextra after: %v", missing, extra)
			}

			// The gate's scope view after the migration, through the same broker declaration the launch
			// uses, grants exactly the write grants the pre-binding gate granted the old files (oldWrites,
			// observed from f7b06d6a5). The read rows are the read scope's and are not compared here.
			github := shippedGitHubScopeBroker(t, decls)
			check := config.NewScopeCheck(ws, []config.ScopeBroker{github}, after)
			if check == nil || check.Fatal != nil {
				t.Fatalf("the gate's scope check failed after the migration: %+v", check)
			}
			gotWrites := map[string]bool{}
			for _, src := range check.Sources {
				for _, g := range src.Grants {
					if g.Kind != brokerscope.KindRead {
						gotWrites[g.String()] = true
					}
				}
			}
			wantWrites := map[string]bool{}
			for _, w := range tc.oldWrites {
				wantWrites[w] = true
			}
			missing, extra = diffGrantSets(wantWrites, gotWrites)
			if len(missing) > 0 || len(extra) > 0 {
				t.Fatalf("the gate's write grants after the migration differ from the pre-binding gate's on the old files\nmissing after: %v\nextra after: %v", missing, extra)
			}

			// A file with no old key keeps its bytes.
			for name, text := range old {
				got, err := os.ReadFile(filepath.Join(ws, name))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(text), "repos = [") && string(got) != string(text) {
					t.Fatalf("%s changed though it has no old key:\n%s", name, got)
				}
			}
		})
	}
}

// remotePairs turns remote URLs into the name, url pairs namedRemotes takes: origin first, then
// upstream-style names.
func remotePairs(urls []string) []string {
	var out []string
	for i, u := range urls {
		name := "origin"
		if i > 0 {
			name = fmt.Sprintf("r%d", i)
		}
		out = append(out, name, u)
	}
	return out
}

// shippedGitHubScopeBroker is the github broker as the shipped pack declares it: the one the
// launch's scope check is built from.
func shippedGitHubScopeBroker(t *testing.T, decls []config.ReadableBrokerDeclaration) config.ScopeBroker {
	t.Helper()
	for i := range decls {
		d := decls[i]
		if d.Brokered.Source == "github" {
			return config.ScopeBroker{Source: "github", Label: "github-broker", RemoteHost: "github.com",
				Brokered: d.Brokered, GrantKinds: nil, Readable: &d}
		}
	}
	t.Fatalf("no shipped github broker declaration")
	return config.ScopeBroker{}
}
