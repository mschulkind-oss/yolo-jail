package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func readablePackTestHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestLoadPackEntriesReadsQualifiedReadablePacks(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, "src", "packs")
	packDir := filepath.Join(root, "tools")
	localRoot := filepath.Join(home, ".config", "yolo-jail", "packs")
	localPackDir := filepath.Join(localRoot, "fixture")
	implicitDir := filepath.Join(home, ".config", "yolo-jail", "local")
	for _, dir := range []string{packDir, localPackDir, implicitDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(packDir, "pack.json"), `{"name":"not-the-handle"}`)
	write(t, filepath.Join(localPackDir, "pack.json"), `{}`)
	write(t, filepath.Join(implicitDir, "pack.json"), `{}`)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = ['yolo/claude', 'src/tools', 'local/fixture']\n[pack_sources]\nsrc = '"+root+"'\n")

	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("readable packs problems = %q", problems)
	}
	if len(entries) != 4 {
		t.Fatalf("readable selection = %#v, want three listed packs and the independent conventional local pack", entries)
	}
	if entries[0].Name != "yolo/claude" || !entries[0].Embedded() || entries[0].EmbeddedName != "claude" {
		t.Errorf("explicit shipped handle = %+v, want yolo/claude resolving to shipped claude", entries[0])
	}
	wire, err := MarshalPacks(entries[:1])
	if err != nil {
		t.Fatal(err)
	}
	fromWire, err := UnmarshalPacks(wire)
	if err != nil || len(fromWire) != 1 || !fromWire[0].Embedded() || fromWire[0].EmbeddedName != "claude" || fromWire[0].Slug() != entries[0].Slug() {
		t.Fatalf("explicit shipped wire roundtrip = %+v, %v (wire %s)", fromWire, err, wire)
	}
	shipped, err := ResolvePack(fromWire[0], ResolvePackSpec{})
	if err != nil || shipped.Pack == nil || shipped.Pack.Name != "claude" {
		t.Fatalf("wire entry did not resolve the canonical shipped pack: %+v, %v", shipped, err)
	}
	stage := filepath.Join(t.TempDir(), "staged")
	staged, err := ResolvePack(fromWire[0], ResolvePackSpec{Dest: stage})
	if err != nil || staged.Pack == nil || staged.Pack.Root != stage || staged.Pack.Name != "claude" {
		t.Fatalf("explicit shipped staged resolve lost canonical identity/handle: %+v, %v", staged, err)
	}

	if entries[1].Name != "src/tools" || !entries[1].IsLocal() {
		t.Errorf("qualified local handle = %+v", entries[1])
	}
	resolvedSource, err := ResolvePack(entries[1], ResolvePackSpec{})
	if err != nil || resolvedSource.Pack == nil || resolvedSource.Pack.Name != "src/tools" {
		t.Fatalf("listed source pack did not resolve with its authored handle: %+v, %v", resolvedSource, err)
	}
	wantLocalPath := filepath.Join(localRoot, "fixture")
	wantPhysical, err := filepath.EvalSymlinks(wantLocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if entries[2].Name != "local/fixture" || entries[2].LocalPath != wantLocalPath ||
		entries[2].PhysicalPath != wantPhysical ||
		entries[2].Source != "file://"+filepath.ToSlash(wantLocalPath) {
		t.Errorf("local/name did not use the named /packs source: %+v, want path %s", entries[2], wantLocalPath)
	}
	resolved, err := ResolvePack(entries[2], ResolvePackSpec{})
	if err != nil || resolved.Pack == nil {
		t.Fatalf("local/name did not resolve through the production resolver: %+v, %v", resolved, err)
	}
	if entries[3].Name != LocalPackName || !entries[3].Implicit || entries[3].PhysicalPath != "" ||
		entries[3].Source != "file://"+filepath.ToSlash(implicitDir) {
		t.Errorf("conventional local content pack was not independently appended: %+v", entries[3])
	}

	selected, complete := SelectedPackDeclarations(entries, UserScopeSelection(), nil)
	if !complete {
		t.Fatal("the real selected-pack closure did not resolve the listed source entry")
	}
	var names []string
	foundLocal, foundNamedLocal := false, false
	for _, pack := range selected {
		names = append(names, pack.Name)
		if pack.Name == "src/tools" {
			foundLocal = true
		}
		if pack.Name == "local/fixture" {
			foundNamedLocal = true
		}
	}
	if !foundLocal || !foundNamedLocal {
		t.Errorf("selected declarations omit explicitly listed source packs: %v", names)
	}
}

func TestLoadPackEntriesRejectsPhysicalDirectoryAliasesWithBothOrigins(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, "packs")
	packDir := filepath.Join(root, "one")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = [\n  'a/one',\n  'b/one',\n]\n[pack_sources]\na = '"+root+"'\nb = '"+alias+"'\n")

	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(problems) != 1 || !strings.Contains(problems[0], "duplicate local pack directory") {
		t.Fatalf("duplicate physical source result = entries %v, problems %q", entries, problems)
	}
	if !strings.Contains(problems[0], paths.UserConfigTOMLName+":2:3") || !strings.Contains(problems[0], paths.UserConfigTOMLName+":3:3") {
		t.Errorf("duplicate diagnostic lacks both physical source locations: %q", problems[0])
	}
}

func TestLoadPackEntriesKeepsLegacyPackReferences(t *testing.T) {
	home := readablePackTestHome(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), `{"packs":["claude"]}`)
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 || entries[0].Name != "claude" || !entries[0].Embedded() {
		t.Fatalf("legacy packs = %#v, %q, %v", entries, problems, err)
	}
}

func TestLoadReadablePacksRejectsReservedYoloSourceName(t *testing.T) {
	home := readablePackTestHome(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = ['yolo/claude']\n[pack_sources]\nyolo = '/tmp/anything'\n")
	_, problems, err := LoadPackEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], paths.UserConfigTOMLName+":3:") ||
		!strings.Contains(problems[0], "pack_sources.yolo") {
		t.Fatalf("reserved yolo source result = %q, want source line provenance", problems)
	}
}

func configPackGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReadableDefaultPackResolvesLockedCommitAfterFreshProcessAndMirrorMove(t *testing.T) {
	home := readablePackTestHome(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "packs.git")
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	configPackGit(t, base, "init", "-q", "--bare", "--initial-branch=trunk", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	configPackGit(t, work, "init", "-q", "--initial-branch=trunk")
	packDir := filepath.Join(work, "tools")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(packDir, "pack.json"), `{}`)
	write(t, filepath.Join(packDir, "version"), "first\n")
	configPackGit(t, work, "add", "-A")
	configPackGit(t, work, "commit", "-qm", "first")
	first := configPackGit(t, work, "rev-parse", "HEAD")
	configPackGit(t, work, "remote", "add", "origin", "file://"+filepath.ToSlash(remote))
	configPackGit(t, work, "push", "-q", "origin", "trunk")
	configPackGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/trunk")

	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = ['acme/tools']\n[pack_sources.acme]\ngit = 'file://"+filepath.ToSlash(remote)+"'\n")
	entries, err := LoadPacks(nil)
	entries = withoutDefaultSelected(entries)
	if err != nil || len(entries) != 1 || !entries[0].AllowDefaultRef {
		t.Fatalf("readable pack entries = %v, %v", entries, err)
	}
	entry := entries[0]
	lockPath := packsrc.LockPath(paths.UserConfigPath())
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	outcomes, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: lockPath})
	if err != nil || len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Commit != first || outcomes[0].Ref != "trunk" {
		t.Fatalf("first refresh = %+v, %v; want locked trunk at %s", outcomes, err, first)
	}

	write(t, filepath.Join(work, "next.txt"), "next\n")
	configPackGit(t, work, "add", "-A")
	configPackGit(t, work, "commit", "-qm", "review")
	second := configPackGit(t, work, "rev-parse", "HEAD")
	configPackGit(t, work, "branch", "alice/review", second)
	configPackGit(t, work, "push", "-q", "origin", "alice/review")
	configPackGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/alice/review")
	probeSource := "git+file://" + filepath.ToSlash(remote) + "?ref=alice%2Freview"
	probe, err := store.Refresh([]packsrc.RefreshPack{{Name: "probe", Source: probeSource}}, packsrc.RefreshOptions{
		Force: true, LockPath: lockPath,
	})
	if err != nil || len(probe) != 1 || probe[0].Err != nil || probe[0].Commit != second {
		t.Fatalf("moving the mirror's default ref = %+v, %v; want probe commit %s", probe, err, second)
	}

	// A new store models a fresh process: the old decision cache is gone and the
	// omitted authored source must be resolved from packs.lock.json, not mirror HEAD.
	resolved, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err != nil || resolved.Pack == nil {
		t.Fatalf("fresh read-only default-pack resolution = %+v, %v", resolved, err)
	}
	version, err := os.ReadFile(filepath.Join(resolved.Pack.Root, "version"))
	if err != nil || string(version) != "first\n" {
		t.Fatalf("fresh resolution did not use locked content: %q, %v", version, err)
	}
	if entry.Source != "git+file://"+filepath.ToSlash(remote)+"//tools" {
		t.Errorf("resolving the pin rewrote authored source: %q", entry.Source)
	}
	selected, complete := SelectedPackDeclarations(entries, UserScopeSelection(), nil)
	if !complete || len(selected) != 1 || selected[0].Name != entry.Name {
		t.Fatalf("read-only selected declarations = %v, complete %v", selected, complete)
	}
	selectedVersion, err := os.ReadFile(filepath.Join(selected[0].Root, "version"))
	if err != nil || string(selectedVersion) != "first\n" {
		t.Fatalf("selected declarations did not preserve locked content: %q, %v", selectedVersion, err)
	}

	addr, err := entry.PackAddress()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	for _, tc := range []struct {
		name string
		seed *packsrc.Lock
	}{
		{name: "missing pin"},
		{name: "wrong source pin", seed: func() *packsrc.Lock {
			lock := &packsrc.Lock{}
			lock.Set(packsrc.LockEntry{Name: entry.Name, Source: entry.Source + "?ref=trunk", Ref: "trunk", Commit: first})
			return lock
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrongLockPath := filepath.Join(t.TempDir(), "packs.lock.json")
			if tc.seed != nil {
				if err := tc.seed.Save(wrongLockPath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := resolver.ResolvePackDefault(addr, entry.Name, entry.Slug(), entry.Source, wrongLockPath, true); err == nil || !strings.Contains(err.Error(), "no matching default-branch pin") {
				t.Fatalf("missing or mismatched lock was guessed from the moved mirror: %v", err)
			}
		})
	}
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolvePackDefault(addr, entry.Name, entry.Slug(), entry.Source, lockPath, true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || string(after) != string(before) {
		t.Errorf("read-only default resolution changed its lock: %v", err)
	}
}

func TestResolveReadableDefaultPinUsesDeliveredSlugWhenStoreIsMissing(t *testing.T) {
	home := readablePackTestHome(t)
	stagedRoot := filepath.Join(t.TempDir(), "delivered")
	entry := PackEntry{
		Source: "git+https://example.invalid/acme/packs//tools", Name: "acme/tools",
		SourceID: "acme", AllowDefaultRef: true,
	}
	stagedPack := filepath.Join(stagedRoot, entry.Slug())
	if err := os.MkdirAll(stagedPack, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stagedPack, "pack.json"), `{}`)
	lock := &packsrc.Lock{}
	lock.Set(packsrc.LockEntry{Name: entry.Name, Source: entry.Source, Ref: "trunk", Commit: strings.Repeat("a", 40)})
	if err := lock.Save(packsrc.LockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
	for _, readOnly := range []bool{true, false} {
		t.Run(fmt.Sprintf("readOnly=%t", readOnly), func(t *testing.T) {
			resolved, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: readOnly, Getenv: func(key string) string {
				if key == "YOLO_PACK_ROOT" {
					return stagedRoot
				}
				return ""
			}})
			if err != nil || resolved.Pack == nil || resolved.StagedFrom != stagedPack {
				t.Fatalf("valid-pin delivered fallback = %+v, %v; want %s", resolved, err, stagedPack)
			}
		})
	}
	if _, err := os.Stat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Errorf("valid-pin staged fallback created a store in %s (home %s): %v", paths.PacksDir(), home, err)
	}
}

func TestReadableNamedShippedSourceIsNotEmbedded(t *testing.T) {
	for _, kind := range []string{"local", "git"} {
		t.Run(kind, func(t *testing.T) {
			home := readablePackTestHome(t)
			packDir := filepath.Join(t.TempDir(), "tools")
			if err := os.MkdirAll(packDir, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(packDir, "pack.json"), `{}`)
			write(t, filepath.Join(packDir, "source-kind"), kind)
			var sourceConfig string
			if kind == "local" {
				sourceConfig = "packs = ['shipped/tools']\n[pack_sources]\nshipped = '" + filepath.ToSlash(filepath.Dir(packDir)) + "'\n"
			} else {
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				remote := filepath.Join(base, "shipped.git")
				work := filepath.Join(base, "work")
				if err := os.MkdirAll(remote, 0o755); err != nil {
					t.Fatal(err)
				}
				configPackGit(t, base, "init", "-q", "--bare", "--initial-branch=trunk", remote)
				if err := os.MkdirAll(work, 0o755); err != nil {
					t.Fatal(err)
				}
				configPackGit(t, work, "init", "-q", "--initial-branch=trunk")
				if err := os.MkdirAll(filepath.Join(work, "tools"), 0o755); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(work, "tools", "pack.json"), `{}`)
				write(t, filepath.Join(work, "tools", "source-kind"), kind)
				configPackGit(t, work, "add", "-A")
				configPackGit(t, work, "commit", "-qm", "first")
				configPackGit(t, work, "remote", "add", "origin", "file://"+filepath.ToSlash(remote))
				configPackGit(t, work, "push", "-q", "origin", "trunk")
				configPackGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
				sourceConfig = "packs = ['shipped/tools']\n[pack_sources.shipped]\ngit = 'file://" + filepath.ToSlash(remote) + "'\nref = 'trunk'\n"
			}
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), sourceConfig)
			entries, err := LoadPacks(nil)
			entries = withoutDefaultSelected(entries)
			if err != nil || len(entries) != 1 || entries[0].Embedded() || entries[0].EmbeddedName != "" {
				t.Fatalf("named shipped source was classified as embedded: %+v, %v", entries, err)
			}
			entry := entries[0]
			if !entry.IsLocal() {
				outcomes, err := (&packsrc.Store{Dir: paths.PacksDir()}).Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{
					LockPath: packsrc.LockPath(paths.UserConfigPath()),
				})
				if err != nil || len(outcomes) != 1 || outcomes[0].Err != nil {
					t.Fatalf("refresh named shipped git source = %+v, %v", outcomes, err)
				}
			}
			resolved, err := ResolvePack(entry, ResolvePackSpec{Getenv: func(string) string { return "" }})
			if err != nil || resolved.Pack == nil {
				t.Fatalf("named shipped source did not resolve through its configured %s source: %+v, %v", kind, resolved, err)
			}
			got, err := os.ReadFile(filepath.Join(resolved.Pack.Root, "source-kind"))
			if err != nil || string(got) != kind {
				t.Fatalf("resolved named shipped source content = %q, %v; want %s", got, err, kind)
			}
		})
	}
}

func TestResolveDefaultRefPackUsesDeliveredTreeWithoutHostLock(t *testing.T) {
	readablePackTestHome(t)
	stagedRoot := filepath.Join(t.TempDir(), "delivered")
	entry := PackEntry{
		Source: "git+https://example.invalid/acme/packs//tools", Name: "acme/tools",
		SourceID: "acme", AllowDefaultRef: true,
	}
	stagedPack := filepath.Join(stagedRoot, entry.Slug())
	if err := os.MkdirAll(stagedPack, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stagedPack, "pack.json"), `{}`)
	res, err := ResolvePack(entry, ResolvePackSpec{Getenv: func(key string) string {
		if key == "YOLO_PACK_ROOT" {
			return stagedRoot
		}
		return ""
	}})
	if err != nil || res.Pack == nil || res.StagedFrom != stagedPack {
		t.Fatalf("default-ref source did not use delivered tree: %+v, %v", res, err)
	}
	if _, err := os.Stat(packsrc.LockPath(paths.UserConfigPath())); !os.IsNotExist(err) {
		t.Errorf("staged-only default-ref resolution wrote or required host lock metadata: %v", err)
	}
	if _, err := os.Stat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Errorf("staged-only default-ref resolution created a host pack store: %v", err)
	}
}

func TestLoadReadablePacksRejectsBarePathsUnknownSourcesAndMalformedRefs(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{"bare path", "packs = ['/tmp/not-a-qualified-pack']\n", "expected source/pack"},
		{"unknown source", "packs = ['missing/tools']\n", "unknown pack source"},
		{"empty ref", "packs = ['acme/tools']\n[pack_sources.acme]\ngit = 'https://example.invalid/acme/packs'\nref = ''\n", "expected a non-empty string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := readablePackTestHome(t)
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), tc.config)
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 || len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("invalid readable pack config = entries %v, problems %q; want %q", entries, problems, tc.want)
			}
			if !strings.Contains(problems[0], paths.UserConfigTOMLName+":") {
				t.Errorf("refusal lacks TOML provenance: %q", problems[0])
			}
		})
	}
}

func TestReadableTOMLRejectsLegacyPackAliases(t *testing.T) {
	home := readablePackTestHome(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = [{ source = 'git+https://example.invalid/acme/tools?ref=main', name = 'fixture' }]\n")
	if _, _, err := LoadPackEntries(); err == nil || !strings.Contains(err.Error(), "unknown TOML config key") {
		t.Fatalf("legacy source/name fields were accepted as TOML pack syntax: %v", err)
	}
}

func TestReadablePackSourceDirectoryDoesNotSelectEveryPack(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, "packs")
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = []\n[pack_sources]\nsrc = '"+root+"'\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 0 {
		t.Fatalf("unselected source directory was activated: entries %v, problems %q, err %v", entries, problems, err)
	}
}
