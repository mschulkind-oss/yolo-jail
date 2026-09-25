package config

// renderedhostfiles_test.go pins RenderedHostFilePaths — which host_files destinations a
// launch would actually render. It feeds a refusal with no escape hatch, so the cases that
// must NOT count carry as much weight as the ones that must: each is a grant a launch
// would render nothing for, and refusing over it is a false positive (OQ-SSO8).

import (
	"path/filepath"
	"sort"
	"testing"
)

// renderedSet runs RenderedHostFilePaths over a merged map, for a backend that delivers
// directory entries (podman), and returns the result as a set.
func renderedSet(t *testing.T, merged string) map[string]bool {
	t.Helper()
	return renderedSetFor(t, merged, true)
}

// renderedSetFor is renderedSet with the backend's directory answer spelled out.
func renderedSetFor(t *testing.T, merged string, dirsDeliver bool) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, p := range RenderedHostFilePaths(decode(t, merged), dirsDeliver) {
		got[p] = true
	}
	return got
}

// TestRenderedHostFilePathsCountsWhatLands: source-less entries always land; a
// source-bearing entry lands when its host source exists as the declared kind, or when it
// carries a layer the surface falls back to — and never when it renders nothing.
func TestRenderedHostFilePathsCountsWhatLands(t *testing.T) {
	home := userConfigHome(t, `{"host_files": [
	  "~/.widget/",
	  "~/.tool/conf.json",
	  "~/.missing/conf.json",
	  "~/.missingdir/",
	  {"path": "~/.layered/conf.json", "source": "~/.layered/conf.json", "defaults": {"a": 1}}
	]}`)
	mkdir(t, filepath.Join(home, ".widget"))
	write(t, filepath.Join(home, ".tool", "conf.json"), "{}")

	got := renderedSet(t, `{"host_files": [
	  {"path": "~/.inline/conf.json", "content": "{}"},
	  "~/.from-workspace/secret"
	]}`)

	for _, want := range []string{
		".widget",            // a directory entry over an existing directory
		".tool/conf.json",    // a file entry over an existing file
		".layered/conf.json", // absent source, but a defaults layer the surface falls back to
		".inline/conf.json",  // source-less: always lands
	} {
		if !got[want] {
			t.Errorf("%s would render and was not counted; got %v", want, keys(got))
		}
	}
	for _, not := range []string{
		".missing/conf.json",     // source absent: the mount side skips the bind
		".missingdir",            // the same, for a directory entry
		".from-workspace/secret", // source-bearing via the merged map: LoadHostFiles never reads it
	} {
		if got[not] {
			t.Errorf("%s renders nothing and was counted; got %v", not, keys(got))
		}
	}
}

// TestRenderedHostFilePathsSkipsAKindMismatch: a trailing-`/` entry over a FILE, or a file
// entry over a DIRECTORY, is skipped by the mount side (isDir / isFile), so it renders
// nothing and must not count.
func TestRenderedHostFilePathsSkipsAKindMismatch(t *testing.T) {
	home := userConfigHome(t, `{"host_files": ["~/.afile/", "~/.adir"]}`)
	write(t, filepath.Join(home, ".afile"), "x")
	mkdir(t, filepath.Join(home, ".adir"))
	if got := renderedSet(t, `{}`); len(got) != 0 {
		t.Errorf("kind-mismatched sources were counted: %v", keys(got))
	}
}

// TestRenderedHostFilePathsDropsDirectoriesABackendDoesNotDeliver: macos-user never copies
// a directory entry and Apple Container below the read-only-bind floor declines the bind, so
// on those backends a directory grant over an EXISTING directory still renders nothing. The
// canonical `~/.aws/` grant is the case: counting it there refused a launch over a ~/.aws the
// sandbox never got. File entries on the same backend are unaffected — both copy files.
func TestRenderedHostFilePathsDropsDirectoriesABackendDoesNotDeliver(t *testing.T) {
	home := userConfigHome(t, `{"host_files": ["~/.aws/", "~/.tool/conf.json"]}`)
	mkdir(t, filepath.Join(home, ".aws"))
	write(t, filepath.Join(home, ".tool", "conf.json"), "{}")
	inline := `{"host_files": [{"path": "~/.inline/conf.json", "content": "{}"}]}`

	got := renderedSetFor(t, inline, false)
	if got[".aws"] {
		t.Errorf("a directory entry was counted on a backend that delivers no directories; got %v",
			keys(got))
	}
	for _, want := range []string{".tool/conf.json", ".inline/conf.json"} {
		if !got[want] {
			t.Errorf("%s still renders on that backend and was not counted; got %v", want, keys(got))
		}
	}
	// The control: the same config on a backend that binds directories counts it.
	if got := renderedSetFor(t, inline, true); !got[".aws"] {
		t.Errorf("the directory entry must count where directories are delivered; got %v", keys(got))
	}
}

// TestRenderedHostFilePathsUnreadableUserConfigIsNil: "I could not look" is never a grant.
func TestRenderedHostFilePathsUnreadableUserConfigIsNil(t *testing.T) {
	userConfigHome(t, `{"host_files": [`)
	if got := RenderedHostFilePaths(decode(t, `{"host_files": [{"path": "~/.x", "content": ""}]}`), true); got != nil {
		t.Errorf("an unparseable user config produced grants: %v", got)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
