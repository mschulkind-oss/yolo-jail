package run

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// G14's host half: the overlay builder reports exactly what it DELIVERED, and those
// destinations reach the plan, where the Seatbelt profile write-protects them. The profile's
// own rendering and path resolution are pinned in internal/macosuser; what is pinned here is
// that the list starts from the pack declarations and survives every hop to the backend.

// The destinations are the ones WRITTEN, once each — not the declared list. A skills target
// nothing was staged for is neither delivered nor protected, and two packs merging into one
// destination are one destination. And they are the SAME list the sandbox install reads: what
// the builder returns for the profile is the destination list it wrote beside the tree, so the
// paths Seatbelt protects and the paths the bootstrap replaces cannot come apart.
func TestHomeOverlayReturnsTheDestinationsItWrote(t *testing.T) {
	staging := t.TempDir()
	for _, dir := range []string{"skills-claude", "skills-shared"} {
		if err := os.MkdirAll(filepath.Join(staging, dir, "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, briefingStagingName(".claude/CLAUDE.md")),
		[]byte("briefing"), 0o644); err != nil {
		t.Fatal(err)
	}

	tree, dests, err := buildMacosHomeOverlayFor(staging, []jailcontent.SkillTarget{
		{Staging: "skills-claude", Dest: ".claude/skills"},
		{Staging: "skills-shared", Dest: ".claude/skills"}, // merged into the same destination
		{Staging: "skills-absent", Dest: ".codex/skills"},  // declared, nothing staged
	}, []briefingDest{{Into: ".claude/CLAUDE.md"}, {Into: ".codex/AGENTS.md"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tree == "" {
		t.Fatal("no overlay built")
	}
	if want := []string{".claude/CLAUDE.md", ".claude/skills"}; !reflect.DeepEqual(dests, want) {
		t.Errorf("dests = %v, want %v — the written set, deduplicated, as the list sorts", dests, want)
	}
	body, err := os.ReadFile(filepath.Join(tree, entrypoint.HomeOverlayManifestName))
	if err != nil {
		t.Fatalf("the overlay carries no destination list: %v", err)
	}
	var listed struct {
		Destinations []string `json:"destinations"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed.Destinations, dests) {
		t.Errorf("the profile protects %v but the sandbox install replaces %v: one list, two answers",
			dests, listed.Destinations)
	}
}

// The wrapper carries the selection's scope:workspace state dirs beside the tree, because they
// decide where each destination physically lands; and a launch that delivers nothing gets the
// zero value, so the plan protects nothing and stages nothing.
func TestHomeOverlayCarriesTheLayoutItLandsIn(t *testing.T) {
	claude := officialPack(t, "claude")
	staging := t.TempDir()

	if got, err := buildMacosHomeOverlay(staging, []*packload.Pack{claude}, nil); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, macosuser.HomeOverlay{}) {
		t.Errorf("nothing staged, yet the overlay is %#v", got)
	}

	stageEveryDeclaredDest(t, staging, []*packload.Pack{claude})
	got, err := buildMacosHomeOverlay(staging, []*packload.Pack{claude}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tree == "" {
		t.Fatal("no tree for a staged claude pack")
	}
	if !reflect.DeepEqual(got.WorkspaceDirs, packload.WritableDirs([]*packload.Pack{claude})) {
		t.Errorf("WorkspaceDirs = %v, want the selection's scope:workspace dirs %v",
			got.WorkspaceDirs, packload.WritableDirs([]*packload.Pack{claude}))
	}
	for _, want := range []string{".claude/skills", ".claude/CLAUDE.md"} {
		if !containsStr(got.Dests, want) {
			t.Errorf("Dests %v is missing %s", got.Dests, want)
		}
	}
}

// EVERY SHIPPED PACK, ENUMERATED FROM ITS DECLARATIONS rather than from a list written here:
// every skills dir and briefing any shipped pack delivers on macos-user is write-protected at
// the physical path the kernel will see, and every agent's own state directory — and a file
// in it — is not. A pack added tomorrow is in this test the day it ships.
func TestEveryShippedDestinationIsWriteProtectedAndNothingElse(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	staging := t.TempDir()
	declared := stageEveryDeclaredDest(t, staging, packs)
	if len(declared) == 0 {
		t.Fatal("no shipped pack declares a skills or briefing destination — this test would pass vacuously")
	}
	overlay, err := buildMacosHomeOverlay(staging, packs, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string{}, overlay.Dests...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, declared) {
		t.Fatalf("the overlay delivers %v; the shipped packs declare %v", got, declared)
	}

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(ws, ".yolo", "home")
	home := macosuser.SandboxHome()
	rules := macosuser.ResolveHomeReadonly(home, ws, overlay.WorkspaceDirs, overlay.Dests)
	profile := macosuser.SeatbeltProfile(ws, "", nil, rules)
	denied := subpathsIn(t, profile, "home-content-write-deny")

	// Where the kernel will see a destination, derived here WITHOUT the layout deriver: a
	// destination under a scope:workspace state dir, or under the always-linked .config, is in
	// the sidecar at the dot-stripped name; anything else is in the account home.
	physical := func(dest string) string {
		first, rest, _ := strings.Cut(dest, "/")
		if first == ".config" || containsStr(overlay.WorkspaceDirs, first) {
			return filepath.Join(sidecar, strings.TrimPrefix(first, "."), rest)
		}
		return filepath.Join(home, dest)
	}
	for _, d := range declared {
		if p := physical(d); !covered(denied, p) {
			t.Errorf("%s is delivered and NOT write-protected: no deny covers %s, the path "+
				"~/%s resolves to.\ndenied: %v", d, p, d, denied)
		}
	}
	// And nothing more: each agent's own state directory, and a new file in it, stay writable.
	for _, dir := range append([]string{".config"}, overlay.WorkspaceDirs...) {
		root := filepath.Join(sidecar, strings.TrimPrefix(dir, "."))
		for _, p := range []string{root, filepath.Join(root, "yolo-probe-state.json")} {
			if covered(denied, p) {
				t.Errorf("%s (agent state under ~/%s) is write-denied — the deny froze the "+
					"agent, not its instructions.\ndenied: %v", p, dir, denied)
			}
		}
	}
}

// The dispatch half, end to end on Linux: run.Run composes the overlay, hands the backend the
// destinations it delivered, and the plan built from exactly that value write-protects them.
// Fails if the arm stops passing the destinations, if the builder stops reporting them, or if
// the plan stops turning them into rules.
func TestMacosUserLaunchWriteProtectsWhatItDelivers(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var got macosuser.HomeOverlay
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
		overlay macosuser.HomeOverlay, _ macosuser.HostContext,
		_ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		got = overlay
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	for _, want := range []string{".claude/skills", ".claude/CLAUDE.md"} {
		if !containsStr(got.Dests, want) {
			t.Errorf("the backend was not told %s was delivered (Dests %v)", want, got.Dests)
		}
	}
	if !containsStr(got.WorkspaceDirs, ".claude") {
		t.Errorf("WorkspaceDirs %v lacks .claude, so the plan would resolve ~/.claude/skills "+
			"in the account home, a path the kernel never reports", got.WorkspaceDirs)
	}

	plan := macosuser.BuildRunPlan(ws, jsonx.NewOrderedMap(), []string{"claude"}, []string{"claude"},
		"/usr/local/bin/yolo", "", got, macosuser.HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	resolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	denied := subpathsIn(t, plan.Seatbelt, "home-content-write-deny")
	if want := filepath.Join(resolved, ".yolo", "home", "claude", "skills"); !covered(denied, want) {
		t.Errorf("the plan built from the delivered overlay does not protect %s:\n%v", want, denied)
	}
}

// stageEveryDeclaredDest stages content for every skills target and briefing destination the
// packs declare — the state PrepareSkills and refreshJailBriefings leave — and returns the
// declared destinations, sorted and deduplicated.
func stageEveryDeclaredDest(t *testing.T, staging string, packs []*packload.Pack) []string {
	t.Helper()
	set := map[string]bool{}
	for _, tg := range packSkillTargets(packs) {
		if err := os.MkdirAll(filepath.Join(staging, tg.Staging, "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(staging, tg.Staging, "demo", "SKILL.md"),
			[]byte("demo"), 0o644); err != nil {
			t.Fatal(err)
		}
		set[tg.Dest] = true
	}
	for _, d := range briefingDestinations(packs) {
		if err := os.WriteFile(filepath.Join(staging, briefingStagingName(d.Into)),
			[]byte("briefing"), 0o644); err != nil {
			t.Fatal(err)
		}
		set[d.Into] = true
	}
	// `files` trees are not staged: they are copied from the pack tree itself, so every one
	// whose source the pack ships is delivered (and so must be protected).
	for _, tg := range packFilesTargets(packs, nil) {
		if isDir(tg.Src) || isFile(tg.Src) {
			set[tg.Dest] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

var subpathClause = regexp.MustCompile(`\(subpath "((?:[^"\\]|\\.)*)"\)`)

// subpathsIn returns the (subpath …) paths of the deny form that follows the given test id —
// read out of the RENDERED profile, so the assertion is about the SBPL a launch installs.
func subpathsIn(t *testing.T, profile, id string) []string {
	t.Helper()
	i := strings.Index(profile, "#seatbelt-test-id:"+id+"#")
	if i < 0 {
		t.Fatalf("the profile carries no %s rule:\n%s", id, profile)
	}
	// The form ends where its last clause closes it: `…")` then the form's own `)`.
	form := profile[i:]
	end := strings.Index(form, "))\n")
	if end < 0 {
		t.Fatalf("the %s form never closes:\n%s", id, form)
	}
	form = form[:end+2]
	var out []string
	for _, m := range subpathClause.FindAllStringSubmatch(form, -1) {
		out = append(out, m[1])
	}
	return out
}

// covered reports whether p is matched by any of the (subpath …) entries: the entry itself or
// anything below it, by whole path components.
func covered(subpaths []string, p string) bool {
	for _, s := range subpaths {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}
