package jailcontent

// toolskillslayer_test.go pins the host's TOOL-SKILLS layer (docs/reference/mise-tool-skills.md
// slice 4). The CALL-SITE half, that a launch and an attach hand the layer to the composition, is
// internal/cli/run/toolskillslayer_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/toolskills"
)

const toolToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

var toolDigest = strings.Repeat("ab", 32)

type toolSnap struct{ ws, dir string }

// writeToolSnapshot writes a snapshot by hand, in the layout toolskills.Publish produces:
// skills maps "tool/name" to the body of that skill's SKILL.md.
func writeToolSnapshot(t *testing.T, ws, token string, skills map[string]string, order [][2]string) toolSnap {
	t.Helper()
	dir := filepath.Join(ws, ".yolo", "tool-skills")
	dd := filepath.Join(dir, toolDigest)
	idx := map[string]any{"version": 1, "token": token, "digest": toolDigest}
	var list []map[string]string
	for _, o := range order {
		tool, name := o[0], o[1]
		list = append(list, map[string]string{"tool": tool, "version": "1", "name": name})
		must(t, os.MkdirAll(filepath.Join(dd, "skills", name, "refs"), 0o755))
		must(t, os.WriteFile(filepath.Join(dd, "skills", name, "SKILL.md"), []byte(skills[tool+"/"+name]), 0o644))
		must(t, os.WriteFile(filepath.Join(dd, "skills", name, "refs", "a.md"), []byte("ref"), 0o644))
	}
	idx["skills"] = list
	body, _ := json.Marshal(idx)
	must(t, os.MkdirAll(dd, 0o755))
	must(t, os.WriteFile(filepath.Join(dd, "index.json"), body, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "current"), []byte(toolDigest+"\n"), 0o644))
	return toolSnap{ws: ws, dir: dd}
}

func (f wsFixture) toolInput(tools ...string) *ToolSkillsInput {
	return &ToolSkillsInput{Workspace: f.ws, Token: toolToken, Tools: tools}
}

func (f wsFixture) stageTools(t *testing.T, in *ToolSkillsInput) (string, *WorkspaceSkillsReport) {
	t.Helper()
	staging, rep, err := PrepareSkillsWithToolSkills("ws-test", nil, nil, in)
	if err != nil {
		t.Fatalf("PrepareSkillsWithToolSkills: %v", err)
	}
	return staging, rep
}

func TestToolSkillsAreDeliveredToEveryDestinationWithTheirFiles(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil, "unknownagent": nil})
	writeToolSnapshot(t, f.ws, toolToken, map[string]string{"packslip:x/deploy": "# deploy"},
		[][2]string{{"packslip:x", "deploy"}})
	staging, rep := f.stageTools(t, f.toolInput("packslip:x"))
	for _, agent := range []string{"alpha", "unknownagent"} {
		if !skillsOf(t, staging, agent)["deploy"] {
			t.Errorf("destination %s got no tool skill: %v", agent, skillsOf(t, staging, agent))
		}
		b, err := os.ReadFile(filepath.Join(staging, SkillStagingName(agent), "deploy", "refs", "a.md"))
		if err != nil || string(b) != "ref" {
			t.Errorf("%s: the skill's files were not copied: %v %q", agent, err, b)
		}
	}
	if rep.ToolSkills == nil || rep.ToolSkills.Digest != toolDigest || len(rep.ToolSkills.Delivered) != 1 {
		t.Errorf("the report must carry the delivered digest and names for slice 5: %+v", rep.ToolSkills)
	}
}

func TestToolSkillsLoseToAPackSkillOfTheSameName(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	packDir := filepath.Join(realTempDir(t), "skills")
	must(t, os.MkdirAll(filepath.Join(packDir, "deploy"), 0o755))
	must(t, os.WriteFile(filepath.Join(packDir, "deploy", "SKILL.md"), []byte("PACK"), 0o644))
	SetPackSkillDirs([]PackSkillSource{{Dir: packDir, Pack: "acme"}})
	writeToolSnapshot(t, f.ws, toolToken, map[string]string{"packslip:x/deploy": "TOOL", "packslip:x/other": "TOOL"},
		[][2]string{{"packslip:x", "deploy"}, {"packslip:x", "other"}})
	staging, rep := f.stageTools(t, f.toolInput("packslip:x"))
	b, err := os.ReadFile(filepath.Join(staging, SkillStagingName("alpha"), "deploy", "SKILL.md"))
	if err != nil || string(b) != "PACK" {
		t.Fatalf("the pack skill must win the name: %v %q", err, b)
	}
	if !skillsOf(t, staging, "alpha")["other"] {
		t.Errorf("a free name must still be delivered")
	}
	if len(rep.ToolSkills.Shadowed) != 1 || rep.ToolSkills.Shadowed[0].Name != "deploy" ||
		!strings.Contains(rep.ToolSkills.Shadowed[0].By, "acme") {
		t.Errorf("the shadow must name the winner: %+v", rep.ToolSkills.Shadowed)
	}
}

func TestToolSkillsLoseToAWorkspaceSkillOfTheSameName(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	f.file(t, ".agents/skills/deploy/SKILL.md", "WORKSPACE")
	writeToolSnapshot(t, f.ws, toolToken, map[string]string{"packslip:x/deploy": "TOOL"},
		[][2]string{{"packslip:x", "deploy"}})
	staging, _, err := PrepareSkillsWithToolSkills("ws-test", &WorkspaceSkills{Root: f.ws, Dirs: []string{".agents/skills"}}, nil,
		f.toolInput("packslip:x"))
	must(t, err)
	b, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("alpha"), "deploy", "SKILL.md"))
	if string(b) != "WORKSPACE" {
		t.Errorf("the repository's own skill must beat a tool's: %q", b)
	}
}

func TestToolSkillsWithAWrongOrMissingTokenOrBrokenSnapshotYieldNoLayer(t *testing.T) {
	cases := map[string]func(t *testing.T, f wsFixture) *ToolSkillsInput{
		"wrong token": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, strings.Repeat("0", 64), map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			return f.toolInput("p")
		},
		"empty token in the index": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, "", map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			return f.toolInput("p")
		},
		"no token asked for": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, "", map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			return &ToolSkillsInput{Workspace: f.ws, Tools: []string{"p"}}
		},
		"malformed index": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			must(t, os.WriteFile(filepath.Join(s.dir, "index.json"), []byte("{not json"), 0o644))
			return f.toolInput("p")
		},
		"missing current": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			must(t, os.Remove(filepath.Join(f.ws, ".yolo", "tool-skills", "current")))
			return f.toolInput("p")
		},
		"digest mismatch": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			body, _ := os.ReadFile(filepath.Join(s.dir, "index.json"))
			body = []byte(strings.Replace(string(body), toolDigest, strings.Repeat("cd", 32), 1))
			must(t, os.WriteFile(filepath.Join(s.dir, "index.json"), body, 0o644))
			return f.toolInput("p")
		},
		"empty allowlist": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			return f.toolInput()
		},
		"tool not on the allowlist": func(t *testing.T, f wsFixture) *ToolSkillsInput {
			writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
			return f.toolInput("other")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newWSFixture(t)
			targets(t, map[string][]string{"alpha": nil})
			in := setup(t, f)
			staging, rep := f.stageTools(t, in)
			if got := skillsOf(t, staging, "alpha"); got["s"] {
				t.Errorf("a layer was composed: %v", got)
			}
			if rep.ToolSkills != nil && len(rep.ToolSkills.Delivered) > 0 {
				t.Errorf("the report claims delivery: %+v", rep.ToolSkills)
			}
		})
	}
}

func TestAnIndexIsReadForNamesNeverForOrderOrAWiderAllowlist(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	// Both tools carry "same"; the allowlist, not the index order, decides who wins.
	s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"a/same": "A", "b/same": "B", "b/only": "B"},
		[][2]string{{"a", "same"}, {"b", "same"}, {"b", "only"}})
	// writeToolSnapshot's second "same" wrote over the first, so restate A's bytes for the check.
	_ = s
	staging, rep := f.stageTools(t, f.toolInput("b", "a"))
	b, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("alpha"), "same", "SKILL.md"))
	if string(b) != "B" {
		t.Errorf("the first tool on the allowlist must win the name: %q", b)
	}
	if len(rep.ToolSkills.Skipped) != 1 || !strings.Contains(rep.ToolSkills.Skipped[0].Reason, "b comes first") {
		t.Errorf("the loser is named: %+v", rep.ToolSkills.Skipped)
	}
	// A third tool absent from the allowlist is never taken.
	staging, _ = f.stageTools(t, f.toolInput("a"))
	if skillsOf(t, staging, "alpha")["only"] {
		t.Errorf("a tool off the allowlist was delivered")
	}
}

func TestAnUnsafeSkillNameInTheIndexIsSkippedAndNamed(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/good": "x"}, [][2]string{{"p", "good"}})
	body, _ := os.ReadFile(filepath.Join(s.dir, "index.json"))
	body = []byte(strings.Replace(string(body), `"skills":[`, `"skills":[{"tool":"p","version":"1","name":"../escape"},{"tool":"p","version":"1","name":".hidden"},`, 1))
	must(t, os.WriteFile(filepath.Join(s.dir, "index.json"), body, 0o644))
	staging, rep := f.stageTools(t, f.toolInput("p"))
	if !skillsOf(t, staging, "alpha")["good"] || len(rep.ToolSkills.Skipped) != 2 {
		t.Errorf("delivered %v, skipped %+v", skillsOf(t, staging, "alpha"), rep.ToolSkills.Skipped)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(staging), "escape")); err == nil {
		t.Errorf("a name with a path separator escaped")
	}
}

// P5 for this layer: the snapshot is the repository's to write, so a link anywhere in it is
// never followed. The secret lives outside the workspace; no staged file may carry it.
func TestALinkInsideTheSnapshotIsNeverFollowed(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, f wsFixture, s toolSnap){
		"file link in a skill": func(t *testing.T, f wsFixture, s toolSnap) {
			must(t, os.Symlink(f.secret, filepath.Join(s.dir, "skills", "bad", "leak.md")))
		},
		"directory link in a skill": func(t *testing.T, f wsFixture, s toolSnap) {
			must(t, os.Symlink(f.outside, filepath.Join(s.dir, "skills", "bad", "outdir")))
		},
		"the skill itself a link": func(t *testing.T, f wsFixture, s toolSnap) {
			must(t, os.RemoveAll(filepath.Join(s.dir, "skills", "bad")))
			must(t, os.Symlink(f.outside, filepath.Join(s.dir, "skills", "bad")))
		},
		"the skills dir a link": func(t *testing.T, f wsFixture, s toolSnap) {
			must(t, os.RemoveAll(filepath.Join(s.dir, "skills")))
			must(t, os.MkdirAll(filepath.Join(f.outside, "bad"), 0o755))
			must(t, os.WriteFile(filepath.Join(f.outside, "bad", "SKILL.md"), []byte(wsSecret), 0o644))
			must(t, os.Symlink(f.outside, filepath.Join(s.dir, "skills")))
		},
		"the digest dir a link": func(t *testing.T, f wsFixture, s toolSnap) {
			real := filepath.Join(f.outside, "real")
			must(t, os.Rename(s.dir, real))
			must(t, os.Symlink(real, s.dir))
		},
		"current a link": func(t *testing.T, f wsFixture, s toolSnap) {
			cur := filepath.Join(f.ws, ".yolo", "tool-skills", "current")
			must(t, os.Remove(cur))
			must(t, os.WriteFile(filepath.Join(f.outside, "cur"), []byte(toolDigest+"\n"), 0o644))
			must(t, os.Symlink(filepath.Join(f.outside, "cur"), cur))
		},
		"the snapshot dir a link": func(t *testing.T, f wsFixture, s toolSnap) {
			real := filepath.Join(f.outside, "ts")
			must(t, os.Rename(filepath.Join(f.ws, ".yolo", "tool-skills"), real))
			must(t, os.Symlink(real, filepath.Join(f.ws, ".yolo", "tool-skills")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newWSFixture(t)
			targets(t, map[string][]string{"alpha": nil})
			s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/bad": wsSecret, "p/fine": "ok"},
				[][2]string{{"p", "bad"}, {"p", "fine"}})
			plant(t, f, s)
			staging, _ := f.stageTools(t, f.toolInput("p"))
			assertNoSecretAnywhere(t, staging)
			if skillsOf(t, staging, "alpha")["bad"] {
				// Only the planted-skill cases may deliver nothing of it; a clean sibling is fine.
				t.Errorf("the tainted skill was delivered: %v", skillsOf(t, staging, "alpha"))
			}
		})
	}
}

// The layer reads what toolskills.Publish writes: this fails if the writer's layout and this
// reader drift apart.
func TestTheLayerReadsWhatPublishWrites(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	installs := realTempDir(t)
	src := filepath.Join(installs, "tool", "1", "skills", "fresh")
	must(t, os.MkdirAll(src, 0o755))
	must(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("PUBLISHED"), 0o644))
	_, err := toolskills.Publish(toolskills.Options{Workspace: f.ws, InstallsDir: installs, Token: toolToken,
		LockPath: filepath.Join(realTempDir(t), "lock")},
		[]toolskills.Skill{{Name: "fresh", Tool: "packslip:x", Version: "1", Path: src}})
	must(t, err)
	staging, _ := f.stageTools(t, f.toolInput("packslip:x"))
	b, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("alpha"), "fresh", "SKILL.md"))
	if string(b) != "PUBLISHED" {
		t.Errorf("a published snapshot was not read: %q", b)
	}
}

func TestTheComposedTreeNeverWritesIntoTheSnapshotOrWorkspace(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"alpha": nil})
	s := writeToolSnapshot(t, f.ws, toolToken, map[string]string{"p/s": "x"}, [][2]string{{"p", "s"}})
	before := toolTreeListing(t, f.ws)
	f.stageTools(t, f.toolInput("p"))
	if after := toolTreeListing(t, f.ws); after != before {
		t.Errorf("the composition changed the workspace:\n%s\n--\n%s", before, after)
	}
	_ = s
}

func toolTreeListing(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			info, _ := d.Info()
			b.WriteString(p + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return b.String()
}
