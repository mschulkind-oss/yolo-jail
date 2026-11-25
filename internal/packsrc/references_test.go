package packsrc

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestParseSourceDefinitionsAndQualifiedPackReferences(t *testing.T) {
	local := jsonx.NewOrderedMap()
	local.Set("dotfiles", "~/src/yolo-packs")
	git := jsonx.NewOrderedMap()
	git.Set("git", "https://github.com/acme/yolo-packs.git")
	definitions := jsonx.NewOrderedMap()
	localPath, _ := local.Get("dotfiles")
	definitions.Set("dotfiles", localPath)
	definitions.Set("acme", git)

	sources, err := ParseSourceDefinitions(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if got := sources["acme"].Repo; got != "https://github.com/acme/yolo-packs" {
		t.Fatalf("normalized git source = %q", got)
	}

	ref, err := ParsePackReference("dotfiles/matt", sources, "/home/user/.config/yolo-jail/packs", "/home/user", "config.packs[0]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "dotfiles/matt" || ref.SourceID != "dotfiles" || ref.LocalPath != "/home/user/src/yolo-packs/matt" ||
		ref.Source != "file:///home/user/src/yolo-packs/matt" {
		t.Fatalf("local reference = %+v", ref)
	}

	ref, err = ParsePackReference("acme/tools", sources, "", "/home/user", "config.packs[1]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "acme/tools" || ref.SourceID != "acme" || !ref.AllowDefaultRef || ref.Source != "git+https://github.com/acme/yolo-packs//tools" {
		t.Fatalf("tree reference = %+v", ref)
	}

	ref, err = ParsePackReference("local/matt", sources, "/home/user/.config/yolo-jail/packs", "/home/user", "config.packs[2]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "local/matt" || ref.SourceID != "local" || ref.LocalPath != "/home/user/.config/yolo-jail/packs/matt" {
		t.Fatalf("conventional local reference = %+v", ref)
	}
}

func TestParsePackReferenceAllowsOnlyKnownShippedBareName(t *testing.T) {
	got, err := ParsePackReference("claude", nil, "", "/home/user", "config.packs[0]", map[string]bool{"claude": true})
	if err != nil || got.Source != "embedded:claude" || got.Name != "claude" || got.SourceID != "shipped" {
		t.Fatalf("shipped reference = %+v, %v", got, err)
	}
	if _, err := ParsePackReference("not-shipped", nil, "", "/home/user", "config.packs[1]", nil); err == nil {
		t.Fatal("an unshipped bare name was accepted")
	}
}

func TestParseNamedGitSourceAcceptsHostlessFileURL(t *testing.T) {
	definitions := jsonx.NewOrderedMap()
	git := jsonx.NewOrderedMap()
	git.Set("git", "file:///tmp/yolo-packs.git")
	definitions.Set("acme", git)

	sources, err := ParseSourceDefinitions(definitions)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackReference("acme/tools", sources, "", "/home/user", "config.packs[0]", nil)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := ParsePackAddress(ref.Source)
	if err != nil || addr.Repo != "file:///tmp/yolo-packs.git" || addr.Path != "tools" || !ref.AllowDefaultRef {
		t.Fatalf("hostless file source = %+v, parsed %+v, %v", ref, addr, err)
	}
}

func TestParseNamedGitSourceRefFlowsThroughRefreshAndLock(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "packs.git")
	work := filepath.Join(t.TempDir(), "work")
	gitIn(t, t.TempDir(), "init", "--bare", "--initial-branch=trunk", remote)
	gitIn(t, t.TempDir(), "init", "--initial-branch=trunk", work)
	first := commitFile(t, work, "tools/pack.toml", "name = \"tools\"\n")
	gitIn(t, t.TempDir(), "--git-dir="+remote, "fetch", work, "refs/heads/trunk:refs/heads/trunk")
	gitIn(t, t.TempDir(), "--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	gitIn(t, t.TempDir(), "--git-dir="+remote, "tag", "v1.2.3", first)
	second := commitFile(t, work, "tools/next.txt", "next release\n")
	gitIn(t, t.TempDir(), "--git-dir="+remote, "fetch", work, "HEAD:refs/heads/alice/review")

	for _, tc := range []struct {
		name   string
		ref    string
		commit string
	}{
		{name: "branch", ref: "alice/review", commit: second},
		{name: "tag", ref: "v1.2.3", commit: first},
		{name: "commit", ref: second, commit: second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definitions := jsonx.NewOrderedMap()
			git := jsonx.NewOrderedMap()
			git.Set("git", "file://"+filepath.ToSlash(remote))
			git.Set("ref", tc.ref)
			definitions.Set("acme", git)
			sources, err := ParseSourceDefinitions(definitions)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := ParsePackReference("acme/tools", sources, "", "/home/user", "config.packs[0]", nil)
			if err != nil {
				t.Fatal(err)
			}
			wantSource := "git+file://" + (&url.URL{Path: filepath.ToSlash(remote)}).EscapedPath() + "//tools?ref=" + url.QueryEscape(tc.ref)
			if ref.Source != wantSource || ref.AllowDefaultRef {
				t.Fatalf("named git reference = %+v, want source %q with default-ref disabled", ref, wantSource)
			}
			addr, err := ParsePackAddress(ref.Source)
			if err != nil || addr.Ref != tc.ref {
				t.Fatalf("parsed named address = %+v, %v; want ref %q", addr, err, tc.ref)
			}

			store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
			lockPath := filepath.Join(t.TempDir(), "packs.lock.json")
			outcomes, err := store.Refresh([]RefreshPack{{Name: ref.Name, Source: ref.Source, AllowDefaultRef: ref.AllowDefaultRef}}, RefreshOptions{LockPath: lockPath})
			if err != nil || len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Commit != tc.commit || outcomes[0].Ref != tc.ref {
				t.Fatalf("Refresh = %+v, %v; want %s at %s", outcomes, err, tc.ref, tc.commit)
			}
			lock, err := LoadLock(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := lock.Get(ref.Name)
			if !ok || entry.Source != ref.Source || entry.Ref != tc.ref || entry.Commit != tc.commit {
				t.Fatalf("lock entry = %+v, want exact authored source/ref and commit %s", entry, tc.commit)
			}
			addr.Ref = entry.Commit
			resolved, err := store.ResolveExisting(addr, ref.Name)
			if err != nil || resolved.Commit != tc.commit {
				t.Fatalf("lock consumer ResolveExisting = %+v, %v; want %s", resolved, err, tc.commit)
			}
		})
	}
}

func TestParseSourceDefinitionsRejectsCredentialURLAndRawQuery(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret@example.com/acme/yolo-packs",
		"https://example.com/acme/yolo-packs?ref=main",
		"https://example.com/acme/yolo-packs#fragment",
		"file://relative/repo.git",
		"ftp://example.com/acme/yolo-packs",
	} {
		t.Run(raw, func(t *testing.T) {
			definitions := jsonx.NewOrderedMap()
			git := jsonx.NewOrderedMap()
			git.Set("git", raw)
			definitions.Set("acme", git)
			if _, err := ParseSourceDefinitions(definitions); err == nil {
				t.Fatalf("accepted invalid repository URL %q", raw)
			}
		})
	}
}

func TestParseDirectGitPackUsesRepositoryIdentityAndExplicitRef(t *testing.T) {
	object := jsonx.NewOrderedMap()
	object.Set("git", "https://github.com/mschulkind-oss/matt-craft.git")
	object.Set("ref", "alice/review")
	ref, err := ParsePackReference(object, nil, "", "/home/user", "config.packs[0]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "mschulkind-oss/matt-craft" || ref.Source != "git+https://github.com/mschulkind-oss/matt-craft?ref=alice%2Freview" || ref.AllowDefaultRef {
		t.Fatalf("direct git reference = %+v", ref)
	}
	// The manifest name is not part of this contract: identity is derived from the
	// address and the reference contains no field from pack.toml/pack.json.
}

func TestParseDirectGitRejectsNestedRepositoryPath(t *testing.T) {
	object := jsonx.NewOrderedMap()
	object.Set("git", "https://git.example/group/subgroup/repo")
	if _, err := ParsePackReference(object, nil, "", "/home/user", "config.packs[0]", nil); err == nil ||
		!strings.Contains(err.Error(), "owner/repository URL") {
		t.Fatalf("nested direct-git URL error = %v, want explicit owner/repository requirement", err)
	}
}

func TestParsePackReferenceRejectsUnqualifiedAndUnknownHandles(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{"not-shipped", "only shipped packs"},
		{"missing/tools", "unknown pack source"},
		{"../tools", "expected source/pack"},
	} {
		_, err := ParsePackReference(tc.value, nil, "/packs", "/home/user", "config.packs[4]", nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParsePackReference(%v) error = %v, want %q", tc.value, err, tc.want)
		}
	}
}

func TestLocalDirectoryIdentityFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	one, err := LocalDirectoryIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	two, err := LocalDirectoryIdentity(alias)
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("identities differ: %q != %q", one, two)
	}
}
