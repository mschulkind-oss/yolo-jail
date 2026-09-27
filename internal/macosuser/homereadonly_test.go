package macosuser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// G14's macos-user half: the staged skills and briefings are write-protected by the session
// profile. Everything here is a statement about PATHS — which spelling the profile names, and
// where in the profile it names it. Whether the kernel then refuses is the Mac's question
// (integration/macosuserseatbelt_test.go, integration/macosusercontent_test.go); what these
// settle is that the question is asked about the path the kernel will actually see.

const (
	hrHome = "/Users/_yolojail"
	hrWS   = "/Users/Shared/yolo/proj"
	hrSC   = hrWS + "/.yolo/home"
)

// THE TRAP THE PLAN NAMED: `~/.claude` is a symlink into the workspace sidecar on every
// launch that selects the claude pack, and the kernel resolves it before the policy is
// consulted — so a deny on /Users/_yolojail/.claude/skills alone would be in the profile and
// match nothing. The physical spelling is the one that must be there; the account-home one
// rides along for the case where no link stands.
func TestResolveHomeReadonlyNamesThePhysicalPathThroughTheLayout(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, []string{".claude"},
		[]string{".claude/skills", ".claude/CLAUDE.md"})
	want := HomeReadonly{
		Paths: []string{
			hrSC + "/claude/CLAUDE.md",
			hrSC + "/claude/skills",
			hrHome + "/.claude/CLAUDE.md",
			hrHome + "/.claude/skills",
		},
		// The chain the destinations hang from, in BOTH spaces: the layout link itself in the
		// account home, and every directory from the workspace down to the link's target.
		Anchors: []string{
			hrWS + "/.yolo",
			hrSC,
			hrSC + "/claude",
			hrHome + "/.claude",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveHomeReadonly =\n  %#v\nwant\n  %#v", got, want)
	}
}

// A destination one level BELOW the state dir (pi, omp, agy): past the link the walk is in the
// sidecar, so the intermediate directory is a physical anchor — and the account-home spelling
// of it is not, because while the link stands nothing can ever resolve to it.
func TestResolveHomeReadonlyWalksPastTheLinkInTheSidecar(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, []string{".pi"},
		[]string{".pi/agent/skills", ".pi/agent/AGENTS.md"})
	for _, want := range []string{hrSC + "/pi/agent/skills", hrSC + "/pi/agent/AGENTS.md"} {
		if !hasEntry(got.Paths, want) {
			t.Errorf("Paths %v is missing the physical destination %s", got.Paths, want)
		}
	}
	for _, want := range []string{hrSC + "/pi", hrSC + "/pi/agent", hrHome + "/.pi"} {
		if !hasEntry(got.Anchors, want) {
			t.Errorf("Anchors %v is missing %s", got.Anchors, want)
		}
	}
	if hasEntry(got.Anchors, hrHome+"/.pi/agent") {
		t.Errorf("Anchors %v names %s, a path past the layout link that nothing resolves to",
			got.Anchors, hrHome+"/.pi/agent")
	}
}

// The layout's CORE links count, not only the packs' state dirs: `.config` is always linked
// (DeriveDarwinHomeLayout), and opencode's skills live under it with no state dir of their own.
// A resolver that consulted only the WorkspaceDirs list would name the account-home path and
// protect nothing.
func TestResolveHomeReadonlyHonorsTheLayoutsOwnLinks(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, nil, []string{".config/opencode/skills"})
	if !hasEntry(got.Paths, hrSC+"/config/opencode/skills") {
		t.Errorf("Paths %v does not follow the core .config link into the sidecar", got.Paths)
	}
	for _, want := range []string{hrHome + "/.config", hrSC + "/config", hrSC + "/config/opencode"} {
		if !hasEntry(got.Anchors, want) {
			t.Errorf("Anchors %v is missing %s", got.Anchors, want)
		}
	}
}

// A destination no link covers is a real path in the account home, so that spelling is the
// physical one and the chain is the account home's own directories.
func TestResolveHomeReadonlyKeepsAnUnlinkedDestInTheAccountHome(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, nil, []string{".acme/skills"})
	want := HomeReadonly{Paths: []string{hrHome + "/.acme/skills"}, Anchors: []string{hrHome + "/.acme"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveHomeReadonly = %#v, want %#v", got, want)
	}
}

// A destination that IS a layout link is written THROUGH it, so its target is the path.
func TestResolveHomeReadonlyFollowsADestThatIsALink(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, []string{".claude"}, []string{".claude"})
	if !hasEntry(got.Paths, hrSC+"/claude") {
		t.Errorf("Paths %v does not name the link's target", got.Paths)
	}
	if !hasEntry(got.Anchors, hrHome+"/.claude") || !hasEntry(got.Anchors, hrSC) {
		t.Errorf("Anchors %v is missing the link or the chain above its target", got.Anchors)
	}
}

// Pack declarations are validated upstream; one that is not must narrow nothing it was not
// asked to, which means emitting nothing at all rather than a deny somewhere outside the home.
func TestResolveHomeReadonlyDropsDestsThatAreNotHomeRelative(t *testing.T) {
	got := ResolveHomeReadonly(hrHome, hrWS, []string{".claude"},
		[]string{"", "  ", "/etc/passwd", "..", "../escape", "a/../../b", "."})
	if !got.Empty() {
		t.Errorf("unusable destinations produced rules: %#v", got)
	}
}

// THE DARWIN PATH CLASS, reproduced on Linux: the home and the workspace both reached through a
// symlinked directory — /var → /private/var on a Mac, a symlinked TMPDIR here. Every emitted
// path must be the RESOLVED spelling. The home is deliberately NOT created: a path that does not
// exist yet is where EvalSymlinks on the whole path fails, and a fallback to the lexical
// spelling would keep the symlinked prefix the kernel never reports.
func TestResolveHomeReadonlyResolvesASymlinkedBase(t *testing.T) {
	real, link := symlinkedBase(t)
	if err := os.MkdirAll(filepath.Join(real, "ws"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveHomeReadonly(filepath.Join(link, "home"), filepath.Join(link, "ws"),
		[]string{".claude"}, []string{".claude/skills", ".acme/AGENTS.md"})
	all := append(append([]string{}, got.Paths...), got.Anchors...)
	if len(all) == 0 {
		t.Fatal("nothing emitted")
	}
	for _, p := range all {
		if strings.HasPrefix(p, link+"/") {
			t.Errorf("%s is spelled through the symlink %s, which the kernel never reports — "+
				"a rule naming it matches nothing", p, link)
		}
		if !strings.HasPrefix(p, real+"/") {
			t.Errorf("%s is not under the resolved base %s", p, real)
		}
	}
	if want := filepath.Join(real, "ws", ".yolo", "home", "claude", "skills"); !hasEntry(got.Paths, want) {
		t.Errorf("Paths %v is missing the resolved physical skills dir %s", got.Paths, want)
	}
}

// symlinkedBase returns a resolved real directory and a symlink to it, both minted resolved
// where they are made (AGENTS.md's darwin rule) so the test says the same thing on a Mac.
func symlinkedBase(t *testing.T) (real, link string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real = filepath.Join(base, "real")
	link = filepath.Join(base, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	return real, link
}

func hasEntry(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- the rendered SBPL ---

func sampleHomeReadonly() HomeReadonly {
	return ResolveHomeReadonly(hrHome, hrWS, []string{".claude"},
		[]string{".claude/skills", ".claude/CLAUDE.md"})
}

// Both forms are emitted, each with its own id, each naming what ResolveHomeReadonly produced
// and nothing else — and the anchors take ONLY create and unlink, which is the whole difference
// between "cannot be moved aside" and "the agent's state directory is frozen".
func TestSeatbeltProfileEmitsHomeReadonlyDenies(t *testing.T) {
	h := sampleHomeReadonly()
	p := SeatbeltProfile(hrWS, "", nil, h)
	for _, want := range []string{
		";; #seatbelt-test-id:home-content-write-deny#\n(deny file-write*\n",
		";; #seatbelt-test-id:home-content-anchor-deny#\n(deny file-write-create file-write-unlink\n",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n%s", want, p)
		}
	}
	writeBlock := sbplForm(t, p, "home-content-write-deny")
	for _, path := range h.Paths {
		if !strings.Contains(writeBlock, `(subpath "`+path+`")`) {
			t.Errorf("the write deny does not name %s:\n%s", path, writeBlock)
		}
	}
	anchorBlock := sbplForm(t, p, "home-content-anchor-deny")
	for _, a := range h.Anchors {
		if !strings.Contains(anchorBlock, `(literal "`+a+`")`) {
			t.Errorf("the anchor deny does not name %s:\n%s", a, anchorBlock)
		}
		if strings.Contains(writeBlock, `"`+a+`"`) {
			t.Errorf("anchor %s is under the file-write* deny, which would freeze the agent's "+
				"own state directory:\n%s", a, writeBlock)
		}
	}
}

// THE ORDERING THE MECHANISM RESTS ON, as for workspace_readonly: last match wins, so a deny
// above the writable-set allow (which re-opens the whole sandbox home) is inert while still
// appearing in the profile.
func TestSeatbeltProfileHomeReadonlyDeniesFollowTheAllow(t *testing.T) {
	p := SeatbeltProfile(hrWS, "", []string{".git/hooks"}, sampleHomeReadonly())
	mustPrecede(t, p, "(allow file-write*", "#seatbelt-test-id:home-content-write-deny#",
		"the sandbox-home allow would override the content deny")
	mustPrecede(t, p, "(allow file-write*", "#seatbelt-test-id:home-content-anchor-deny#",
		"the sandbox-home allow would override the anchor deny")
	mustPrecede(t, p, "#seatbelt-test-id:workspace-readonly-deny#", "#seatbelt-test-id:home-content-write-deny#",
		"the sibling sits beside readonlyDenies, in the one post-allow position the tests pin")
	deny := strings.Index(p, "#seatbelt-test-id:home-content-write-deny#")
	if after := strings.Index(p[deny:], "(allow file-write"); after >= 0 {
		t.Errorf("a file-write allow follows the content deny by %d bytes — last match wins, so "+
			"it reopens the staged skills\n%s", after, p)
	}
}

// Nothing delivered, nothing rendered: the profile is byte-identical to the one a launch with no
// content always got, so the feature costs a launch without packs nothing.
func TestSeatbeltProfileWithoutHomeReadonlyIsUnchanged(t *testing.T) {
	base := SeatbeltProfile(hrWS, "", nil, HomeReadonly{})
	for _, h := range []HomeReadonly{{}, {Paths: []string{}}, {Paths: []string{"relative/path"}, Anchors: []string{"x"}}} {
		if got := SeatbeltProfile(hrWS, "", nil, h); got != base {
			t.Errorf("profile drifted for %#v:\n%s", h, got)
		}
	}
	if strings.Contains(base, "home-content") {
		t.Errorf("the empty case emits the content block\n%s", base)
	}
}

// Paths reach SBPL as string literals, so they take the quoting every other path gets.
func TestSeatbeltProfileEscapesHomeReadonlyPaths(t *testing.T) {
	p := SeatbeltProfile(hrWS, "", nil, HomeReadonly{Paths: []string{`/Users/_yolojail/a"b\c`}})
	if !strings.Contains(p, `(subpath "/Users/_yolojail/a\"b\\c")`) {
		t.Errorf("home-readonly path not SBPL-escaped\n%s", p)
	}
}

// sbplForm returns the `(deny …)` form that follows the given id's marker, up to its closing
// paren — enough SBPL parsing to ask which block a clause landed in.
func sbplForm(t *testing.T, profile, id string) string {
	t.Helper()
	i := strings.Index(profile, "#seatbelt-test-id:"+id+"#")
	if i < 0 {
		t.Fatalf("no %s marker in the profile\n%s", id, profile)
	}
	rest := profile[i:]
	start := strings.Index(rest, "(deny")
	depth := 0
	for j := start; j < len(rest); j++ {
		switch rest[j] {
		case '"':
			for j++; j < len(rest) && rest[j] != '"'; j++ {
				if rest[j] == '\\' {
					j++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[start : j+1]
			}
		}
	}
	t.Fatalf("the %s form never closes\n%s", id, rest)
	return ""
}

// --- the call site ---

// THE CALL-SITE PIN. Every test above pins a callee; this is the one that fails if BuildRunPlan
// stops resolving the overlay's destinations, or stops handing them to SeatbeltProfile — which
// would restore the writable copy with every other test here still green.
func TestBuildRunPlanWriteProtectsTheDeliveredContent(t *testing.T) {
	plan := BuildRunPlan(hrWS, jsonx.NewOrderedMap(), []string{"claude"}, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{
			Tree:          "/Users/matt/.local/share/yolo-jail/agents/proj/home-overlay",
			Dests:         []string{".claude/skills", ".claude/CLAUDE.md"},
			WorkspaceDirs: []string{".claude"},
		}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)

	if plan.HomeReadonly.Empty() {
		t.Fatal("the plan resolved no home-readonly rules for a launch that delivers content")
	}
	block := sbplForm(t, plan.Seatbelt, "home-content-write-deny")
	for _, want := range []string{hrSC + "/claude/skills", hrSC + "/claude/CLAUDE.md"} {
		if !strings.Contains(block, `(subpath "`+want+`")`) {
			t.Errorf("the run plan's profile does not write-protect %s — is BuildRunPlan still "+
				"handing the overlay's destinations to SeatbeltProfile?\n%s", want, plan.Seatbelt)
		}
	}
	if !strings.Contains(sbplForm(t, plan.Seatbelt, "home-content-anchor-deny"), `(literal "`+hrHome+`/.claude")`) {
		t.Errorf("the run plan's profile does not anchor the ~/.claude layout link\n%s", plan.Seatbelt)
	}
}

// No tree, no delivery, no rule — even with destinations named. The profile protects what the
// bootstrap COPIES, and a launch that stages no overlay copies nothing.
func TestBuildRunPlanProtectsNothingWhenNothingIsDelivered(t *testing.T) {
	plan := BuildRunPlan(hrWS, jsonx.NewOrderedMap(), nil, []string{"bash"}, "/usr/local/bin/yolo", "",
		HomeOverlay{Dests: []string{".claude/skills"}, WorkspaceDirs: []string{".claude"}},
		HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	if strings.Contains(plan.Seatbelt, "home-content") || !plan.HomeReadonly.Empty() {
		t.Errorf("a launch that delivered no overlay grew content rules:\n%s", plan.Seatbelt)
	}
}

// The plan-level half of the darwin class: a workspace reached through a symlink must produce
// content rules spelled with its RESOLVED path, because BuildRunPlan resolves the workspace once
// and every consumer — this one included — must read that value.
func TestBuildRunPlanResolvesTheWorkspaceIntoTheContentRules(t *testing.T) {
	real, link := symlinkedBase(t)
	plan := BuildRunPlan(link, jsonx.NewOrderedMap(), []string{"claude"}, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{
			Tree: "/host/overlay", Dests: []string{".claude/skills"}, WorkspaceDirs: []string{".claude"},
		}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	block := sbplForm(t, plan.Seatbelt, "home-content-write-deny")
	if want := filepath.Join(real, ".yolo", "home", "claude", "skills"); !strings.Contains(block, `"`+want+`"`) {
		t.Errorf("the content deny does not name the resolved sidecar path %s:\n%s", want, block)
	}
	if strings.Contains(plan.Seatbelt, link+"/") {
		t.Errorf("the profile names the symlinked spelling %s\n%s", link, plan.Seatbelt)
	}
}
