package config

// selectedpacks_test.go pins the maintainer's OQ-BH14 ruling
// (docs/design/base-home-legacy-state.md#28-reservation-is-a-rule-about-config-names-not-about-directories):
// name reservation covers only the SELECTED packs' directories, and an unselected pack is
// treated as if it does not exist.
//
// Until the ruling the reservation was every pack yolo ships (packload.Embedded*), so
// `writable_home_dirs: [".codex"]` was refused in a workspace that never selects codex, and a
// `host_files` entry under `~/.codex/` in a claude-only jail was treated as already writable,
// got no staging, and failed EROFS. The run-side half of the host_files case is
// internal/cli/run's TestAHostFilesEntryUnderAnUnselectedPacksDirIsStaged.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// embeddedPacksNamed returns the shipped packs of these names, in this order.
func embeddedPacksNamed(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	byName := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		byName[p.Name] = p
	}
	out := make([]*packload.Pack, 0, len(names))
	for _, n := range names {
		p, ok := byName[n]
		if !ok {
			t.Fatalf("no shipped pack named %q", n)
		}
		out = append(out, p)
	}
	return out
}

// selectionHome isolates the user config and the pack store the selection is resolved
// from, and writes a user config whose `packs` is packsJSON.
func selectionHome(t *testing.T, packsJSON string) string {
	t.Helper()
	home := useProfileKeysHome(t)
	writeUseProfileKeysUserConfig(t, home, packsJSON)
	return home
}

// THE RULING'S OWN EXAMPLE, driven through ValidateConfig — the call both `yolo check` and
// the launch preflight make — so it fails if validateWritableHomeDirs stops resolving the
// selection (a nil selection accepts `.codex` in both halves).
func TestWritableHomeDirCodexIsLegalUntilCodexIsSelected(t *testing.T) {
	cfg := `{"writable_home_dirs": [".codex"]}`

	selectionHome(t, `["claude"]`)
	if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("writable_home_dirs [\".codex\"] was refused in a claude-only workspace — codex is "+
			"not selected, so its directory is an ordinary path (OQ-BH14): %v", errs)
	}

	selectionHome(t, `["claude", "codex"]`)
	errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("with codex selected, want exactly one error refusing .codex, got %d: %v", len(errs), errs)
	}
	for _, want := range []string{"config.writable_home_dirs[0]", "'.codex'", "already managed",
		"the selected pack codex declares it"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the refusal must contain %q (it arrives the day the pack is selected, so it "+
				"names the pack): %s", want, errs[0])
		}
	}
}

// The deriver drops exactly what validation refuses, for the selection it is handed.
func TestWritableHomeDirsDropsOnlyTheSelectedPacksDirs(t *testing.T) {
	cfg := whdConfig([]any{".codex", ".copilot", ".pi-lens"})
	if got := WritableHomeDirs(cfg, embeddedPacksNamed(t, "claude")); !slices.Equal(got,
		[]string{".codex", ".copilot", ".pi-lens"}) {
		t.Errorf("claude-only: got %v, want every entry kept — no selected pack declares them", got)
	}
	if got := WritableHomeDirs(cfg, embeddedPacksNamed(t, "claude", "codex")); !slices.Equal(got,
		[]string{".copilot", ".pi-lens"}) {
		t.Errorf("claude+codex: got %v, want .codex dropped and nothing else", got)
	}
}

// A shared (machine-scope) dir is reserved exactly like a writable one, for the pack that
// selects it and for nobody else.
func TestWritableHomeDirsReservesTheSelectedPacksSharedDirs(t *testing.T) {
	cfg := whdConfig([]any{".claude-shared-credentials"})
	if got := WritableHomeDirs(cfg, embeddedPacksNamed(t, "codex")); len(got) != 1 {
		t.Errorf("codex-only: got %v, want .claude-shared-credentials kept", got)
	}
	if got := WritableHomeDirs(cfg, embeddedPacksNamed(t, "claude")); len(got) != 0 {
		t.Errorf("claude selected: got %v, want its shared dir refused", got)
	}
}

// Core's own claims do not depend on the selection. `.claude` is the one that looks like a
// pack's: it stays reserved in a codex-only workspace because reservedHomeFiles holds
// `.claude/claude.json`, the target of core's `~/.claude.json` redirect, and
// writable_home_dirs reduces every claim to its first segment.
func TestCoreReservationsHoldWithNoPackSelected(t *testing.T) {
	reserved := reservedHomeSegments(nil)
	for _, seg := range []string{".npm-global", ".local", "go", ".config", ".cache", ".ssh", ".yolo",
		".bash_history", ".gitconfig", ".bashrc", ".claude.json", ".claude"} {
		owner, ok := reserved[seg]
		if !ok {
			t.Errorf("%s is core's and must be reserved with no pack selected", seg)
			continue
		}
		if owner != "" {
			t.Errorf("%s is core's, but the reservation names pack %q", seg, owner)
		}
	}
	for _, seg := range []string{".codex", ".copilot", ".gemini", ".pi", ".oh-omp",
		".claude-shared-credentials", ".gemini-shared-credentials", ".pi-shared-npm"} {
		if _, ok := reserved[seg]; ok {
			t.Errorf("%s is reserved with no pack selected — only a selected pack reserves its dirs", seg)
		}
	}
}

// A CONFIGURED pack's directory is reserved too, which the shipped-set reservation never
// did: it covered every pack yolo ships and no pack a user added.
func TestAConfiguredPacksDirIsReserved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mytool")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "mytool", "contributes": [{"kind": "state", "at": ".mytool", "scope": "workspace"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	selectionHome(t, `["claude", "`+root+`"]`)

	packs, complete := resolveSelectedPacks()
	if !complete {
		t.Fatal("a local pack that exists must resolve completely")
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.Name)
	}
	if !slices.Contains(names, "mytool") || !slices.Contains(names, "claude") {
		t.Fatalf("resolved selection %v, want claude and mytool", names)
	}

	errs, _ := ValidateConfig(decode(t, `{"writable_home_dirs": [".mytool"]}`), t.TempDir(), nil)
	if len(errs) != 1 || !strings.Contains(errs[0], "the selected pack mytool declares it") {
		t.Errorf("a configured pack's state dir must be refused, naming the pack; got %v", errs)
	}
}

// The `needs` closure is part of the selection: codex and claude both need openai-auth, so
// a claude selection resolves it too, exactly as the launch's staging adds it.
func TestTheSelectionIncludesTheNeedsClosure(t *testing.T) {
	selectionHome(t, `["claude"]`)
	packs, complete := resolveSelectedPacks()
	if !complete {
		t.Fatal("an embedded-only selection must resolve completely")
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.Name)
	}
	for _, want := range []string{"claude", "openai-auth", "wire-bridge"} {
		if !slices.Contains(names, want) {
			t.Errorf("resolved selection %v is missing %s (claude's needs pull it in)", names, want)
		}
	}
}

// A configured pack the store cannot resolve leaves the selection INCOMPLETE and refuses
// nothing on its account: staging refuses that launch on its own terms, and `yolo check`
// reports the pack in its Packs section.
func TestAnUnresolvablePackRefusesNoWritableHomeDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	selectionHome(t, `["`+missing+`"]`)
	if _, complete := resolveSelectedPacks(); complete {
		t.Error("a pack whose directory does not exist resolved as complete")
	}
	if errs, _ := ValidateConfig(decode(t, `{"writable_home_dirs": [".gone"]}`), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("an unresolvable pack must not turn a writable_home_dirs entry into an error: %v", errs)
	}
	// Nor does an incomplete selection fall back to reserving every SHIPPED pack, the answer
	// resolveSelectedPacks' comment rejects: `.codex` is a shipped pack's dir that nothing
	// in this selection selects, so only that fallback would refuse it.
	if errs, _ := ValidateConfig(decode(t, `{"writable_home_dirs": [".codex"]}`), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("an unresolvable pack made the reservation cover a shipped pack nothing selects: %v", errs)
	}
}

// THE SELECTION IS THE STAGED ONE, `only`/`exclude` included. A configured pack whose filter
// drops its pack.json is staged with no manifest, so the launch binds none of its dirs; the
// reservation must not refuse the one key that would make such a path writable. It used to:
// validation loaded the UNFILTERED pack root, so `yolo check` refused a writable_home_dirs
// entry the launch would have needed, naming a pack that declares nothing in the jail.
func TestAFilteredOutManifestReservesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mytool")
	if err := os.MkdirAll(filepath.Join(root, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "mytool", "contributes": [{"kind": "state", "at": ".mytool", "scope": "workspace"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "s", "SKILL.md"),
		[]byte("---\nname: s\ndescription: a skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `{"writable_home_dirs": [".mytool"]}`

	// Control: unfiltered, the pack declares .mytool and the entry is refused.
	selectionHome(t, `[{"source": "`+root+`"}]`)
	if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 1 {
		t.Fatalf("control: an unfiltered configured pack's state dir must be refused, got %v", errs)
	}

	selectionHome(t, `[{"source": "`+root+`", "only": ["skills"]}]`)
	if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("`only: [\"skills\"]` stages no pack.json, so the launch binds no ~/.mytool; "+
			"refusing writable_home_dirs [\".mytool\"] refuses the only key that makes it "+
			"writable: %v", errs)
	}
	selectionHome(t, `[{"source": "`+root+`", "exclude": ["pack.json"]}]`)
	if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("`exclude: [\"pack.json\"]` stages no manifest either: %v", errs)
	}
}

// host_files' half of the ruling, at the config seam: a dir only an unselected pack declares
// is an ordinary new top-level path, and becomes "already writable" once the pack is
// selected.
func TestStagingForKeysOnTheSelectedPacks(t *testing.T) {
	// Not ~/.codex/config.toml: that is codex's composed SURFACE, which host_files refuses
	// once codex is selected (OQ-BH15; hostfilesselected_test.go), a separate rule from staging.
	entry := HostFileEntry{Path: ".codex/prompts/review.md"}
	if got := entry.StagingFor(embeddedPacksNamed(t, "claude")); got != HostFileStagingWritableDir {
		t.Errorf("claude-only: StagingFor(~/.codex/prompts/review.md) = %v, want a writable subtree — "+
			"no ~/.codex is bound in that jail, so the write would fail EROFS", got)
	}
	if got := entry.StagingFor(embeddedPacksNamed(t, "claude", "codex")); got != HostFileStagingNone {
		t.Errorf("codex selected: StagingFor(~/.codex/prompts/review.md) = %v, want none — codex binds ~/.codex", got)
	}
	// With no pack selected, `.claude` is an ordinary path for staging too. (writable_home_dirs
	// still reserves its segment, for core's redirect target — see
	// TestCoreReservationsHoldWithNoPackSelected — but staging asks "is it bound rw?", and it
	// is not.)
	if got := (HostFileEntry{Path: ".claude/extra.json"}).StagingFor(nil); got != HostFileStagingWritableDir {
		t.Errorf("no packs: StagingFor(~/.claude/extra.json) = %v, want a writable subtree", got)
	}
}

// The config half of "a host_files entry under ~/.codex/ works in a claude-only jail": the
// entry validates clean there, and the resolved entry is the one StagingFor stages.
func TestAHostFilesEntryUnderAnUnselectedPacksDirValidates(t *testing.T) {
	selectionHome(t, `["claude"]`)
	cfg := decode(t, `{"host_files": [{"path": "~/.codex/prompts/review.md", "content": "be brief"}]}`)
	if errs, _ := ValidateConfig(cfg, t.TempDir(), nil); len(errs) != 0 {
		t.Fatalf("a source-less host_files entry under ~/.codex/ was refused in a claude-only "+
			"workspace: %v", errs)
	}
	entries := SourceLessHostFilesFrom(cfg)
	if len(entries) != 1 || entries[0].Path != ".codex/prompts/review.md" {
		t.Fatalf("resolved entries %+v, want the one ~/.codex/prompts/review.md entry", entries)
	}
	if got := entries[0].StagingFor(embeddedPacksNamed(t, "claude")); got != HostFileStagingWritableDir {
		t.Errorf("StagingFor = %v, want a writable subtree", got)
	}
}

// fetchedPackStore makes a real git repository holding manifest as its pack.json, fetches it
// into this HOME's pack store the way `yolo pack install` does (a bare mirror, no tree), and
// returns the pack's source address and the commit its ref names. Real git, because a mocked
// one would pass while the store's real invocations were wrong.
func fetchedPackStore(t *testing.T, manifest string) (source, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "initial")

	source = "git+file://" + repo + "?ref=main"
	addr, err := packsrc.Parse(source)
	if err != nil {
		t.Skipf("the pack grammar does not accept a local git transport: %v", err)
	}
	commit, err = (&packsrc.Store{Dir: paths.PacksDir()}).Sync(addr)
	if err != nil {
		t.Fatalf("fetching the pack into the store: %v", err)
	}
	return source, commit
}

// VALIDATION WRITES NOTHING INTO THE PACK STORE. It used to resolve a configured pack with
// packsrc.Store.Resolve, the launch's resolver, which checks a missing tree out and
// RemoveAll's an incomplete one before re-checking it out, so `yolo check` and every
// validation of a config holding `writable_home_dirs` could rewrite the store. With
// ResolveExisting, a fetched pack whose tree is missing or incomplete is left exactly as it
// was and reserves nothing, the answer resolveSelectedPacks already gives any pack it cannot
// resolve (the launch's staging checks the tree out, and its own deriver reserves the dir).
func TestValidationLeavesThePackStoreAlone(t *testing.T) {
	home := useProfileKeysHome(t)
	// No staged-tree fallback: this jail's own YOLO_PACK_ROOT must not answer for the pack.
	t.Setenv("YOLO_PACK_ROOT", "")
	source, commit := fetchedPackStore(t,
		`{"name": "mytool", "contributes": [{"kind": "state", "at": ".mytool", "scope": "workspace"}]}`)
	writeUseProfileKeysUserConfig(t, home, `[{"source": "`+source+`", "name": "mytool"}]`)
	cfg := `{"writable_home_dirs": [".mytool"]}`
	trees := filepath.Join(paths.PacksDir(), "trees")

	t.Run("no tree", func(t *testing.T) {
		if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 0 {
			t.Errorf("a pack with no tree in the store reserves nothing, so the entry passes: %v", errs)
		}
		if entries, _ := os.ReadDir(trees); len(entries) != 0 {
			t.Errorf("validation checked the fetched pack out into %s: %v", trees, entries)
		}
	})

	t.Run("an incomplete tree", func(t *testing.T) {
		// An interrupted checkout: content present, completion marker missing.
		if err := os.RemoveAll(filepath.Join(trees, commit)); err != nil {
			t.Fatal(err)
		}
		partial := filepath.Join(trees, commit, "pack.json")
		if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(partial, []byte("PARTIAL"), 0o644); err != nil {
			t.Fatal(err)
		}
		if errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil); len(errs) != 0 {
			t.Errorf("a pack whose tree is incomplete reserves nothing, so the entry passes: %v", errs)
		}
		if b, err := os.ReadFile(partial); err != nil || string(b) != "PARTIAL" {
			t.Errorf("validation rewrote the incomplete tree: %q, %v", b, err)
		}
		if entries, _ := os.ReadDir(filepath.Join(trees, commit)); len(entries) != 1 {
			t.Errorf("validation added to the incomplete tree: %v", entries)
		}
	})

	t.Run("a complete tree still reserves", func(t *testing.T) {
		// Control: once a launch has checked the tree out, validation reads it where it sits.
		if err := os.RemoveAll(filepath.Join(trees, commit)); err != nil {
			t.Fatal(err)
		}
		addr, err := packsrc.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (&packsrc.Store{Dir: paths.PacksDir()}).Resolve(addr, "mytool"); err != nil {
			t.Fatalf("checking the tree out as a launch does: %v", err)
		}
		errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil)
		if len(errs) != 1 || !strings.Contains(errs[0], "the selected pack mytool declares it") {
			t.Errorf("with the tree checked out, the fetched pack's dir must be reserved; got %v", errs)
		}
	})
}
