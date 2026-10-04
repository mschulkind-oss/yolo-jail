package packsrc

// series_test.go pins the series read (series.go; docs/design/patched-forks.md §3.2, PF-D2): its
// members, order, base and digest, and each refusal with the fix it names.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSeriesReadsTheMembersInOrderWithTheirBase(t *testing.T) {
	u := newPatchedUpstream(t)
	// Neither a non-.patch file nor a subdirectory is read.
	writeTestFile(t, filepath.Join(u.pack, "patches", "README.md"), "how to export\n")
	if err := os.MkdirAll(filepath.Join(u.pack, "patches", "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := u.series(t)
	var names []string
	for _, m := range s.Members {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, " "); got != "0001-c-f.txt.patch 0002-c-f.txt.patch" {
		t.Fatalf("members = %q, want the two format-patch files in order", got)
	}
	if s.Base != u.base {
		t.Errorf("base = %s, want %s from the first member's base-commit line", s.Base, u.base)
	}
	// THE DIGEST is the sha256 of the JSON [name, content sha256] list, recomputed here from the
	// files themselves.
	var pairs [][2]string
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(u.pack, "patches", n))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		pairs = append(pairs, [2]string{n, hex.EncodeToString(sum[:])})
	}
	canonical, _ := json.Marshal(pairs)
	sum := sha256.Sum256(canonical)
	if want := hex.EncodeToString(sum[:]); s.Digest != want {
		t.Errorf("digest = %s, want %s", s.Digest, want)
	}
	// A rename changes it: the digest is a function of what the directory lists.
	if err := os.Rename(filepath.Join(u.pack, "patches", names[1]), filepath.Join(u.pack, "patches", "0002-renamed.patch")); err != nil {
		t.Fatal(err)
	}
	if again := u.series(t); again.Digest == s.Digest {
		t.Error("renaming a member left the series digest unchanged")
	}
}

func TestReadSeriesRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, pack string)
		rel  string
		want []string
	}{
		{"missing directory", func(t *testing.T, pack string) {
			os.RemoveAll(filepath.Join(pack, "patches"))
		}, "patches", []string{"does not exist", "create the directory"}},
		{"missing parent", func(t *testing.T, pack string) {}, "nope/patches",
			[]string{"nope", "does not exist"}},
		{"empty directory", func(t *testing.T, pack string) {
			for _, n := range patchNames(t, pack) {
				os.Remove(filepath.Join(pack, "patches", n))
			}
		}, "patches", []string{"holds no .patch file", "git format-patch --base", "plain fork"}},
		{"the directory is a link", func(t *testing.T, pack string) {
			os.Rename(filepath.Join(pack, "patches"), filepath.Join(pack, "real"))
			os.Symlink("real", filepath.Join(pack, "patches"))
		}, "patches", []string{"symbolic link", "regular directory"}},
		{"a link on its path", func(t *testing.T, pack string) {
			os.MkdirAll(filepath.Join(pack, "real"), 0o755)
			os.Rename(filepath.Join(pack, "patches"), filepath.Join(pack, "real", "patches"))
			os.Symlink("real", filepath.Join(pack, "via"))
		}, "via/patches", []string{"via: is a symbolic link"}},
		{"a member is a link", func(t *testing.T, pack string) {
			n := patchNames(t, pack)[1]
			os.Rename(filepath.Join(pack, "patches", n), filepath.Join(pack, "elsewhere.patch"))
			os.Symlink("../elsewhere.patch", filepath.Join(pack, "patches", n))
		}, "patches", []string{"symbolic link", "patch file itself"}},
		{"a member is a directory", func(t *testing.T, pack string) {
			os.MkdirAll(filepath.Join(pack, "patches", "0003-dir.patch"), 0o755)
		}, "patches", []string{"0003-dir.patch", "not a regular file"}},
		{"a plain diff", func(t *testing.T, pack string) {
			writeTestFile(t, filepath.Join(pack, "patches", "0003-plain.patch"),
				"--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-1\n+one\n")
		}, "patches", []string{"0003-plain.patch", "not a mail-format patch", "git format-patch"}},
		{"no base-commit line", func(t *testing.T, pack string) {
			p := filepath.Join(pack, "patches", patchNames(t, pack)[0])
			data, _ := os.ReadFile(p)
			var kept []string
			for _, l := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(l, "base-commit: ") {
					kept = append(kept, l)
				}
			}
			os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644)
		}, "patches", []string{"names no base commit", "--base"}},
		{"a cover letter", func(t *testing.T, pack string) {
			writeTestFile(t, filepath.Join(pack, "patches", "0000-cover-letter.patch"),
				"From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n"+
					"Subject: [PATCH 0/2] *** SUBJECT HERE ***\n\nbase-commit: "+strings.Repeat("a", 40)+"\n")
		}, "patches", []string{"0000-cover-letter.patch", "carries no diff"}},
		{"an unclean path", func(t *testing.T, pack string) {}, "patches/", []string{"not a clean path"}},
		{"an escaping path", func(t *testing.T, pack string) {}, "../patches", []string{"not a clean path"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newPatchedUpstream(t)
			tc.edit(t, u.pack)
			_, err := ReadSeries(u.pack, tc.rel)
			var se *SeriesError
			if !errors.As(err, &se) {
				t.Fatalf("ReadSeries = %v, want a SeriesError", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal lacks %q:\n%s", w, err)
				}
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the refusal is not one line: %q", err)
			}
		})
	}
}

// patchNames lists the fixture's series members in order.
func patchNames(t *testing.T, pack string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(pack, "patches"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".patch") {
			out = append(out, e.Name())
		}
	}
	return out
}

func writeTestFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFollowAndVersions(t *testing.T) {
	for in, want := range map[string]string{"": "release", "release": "release", "head": "head",
		"release:pi-coding-agent@": "release:pi-coding-agent@"} {
		f, err := ParseFollow(in)
		if err != nil || f.String() != want {
			t.Errorf("ParseFollow(%q) = %v, %v; want %s", in, f, err, want)
		}
	}
	for _, bad := range []string{"latest", "release:", "Head", "release:a b", "release:x~"} {
		if _, err := ParseFollow(bad); err == nil {
			t.Errorf("ParseFollow(%q) accepted", bad)
		}
	}
	rel, _ := ParseFollow("release")
	pre, _ := ParseFollow("release:pkg@")
	for _, tc := range []struct {
		f    FollowRule
		tag  string
		want string // "" = not a version
	}{
		{rel, "v1.2.3", "1.2.3"}, {rel, "1.2.3", "1.2.3"}, {rel, "v1.0.0-rc.1", "1.0.0-rc.1"},
		{rel, "v1.0.0+build.5", "1.0.0"}, {rel, "v01.2.3", ""}, {rel, "v1.2", ""}, {rel, "nightly", ""},
		{rel, "pkg@1.2.3", ""}, {pre, "pkg@1.2.3", "1.2.3"}, {pre, "v1.2.3", ""}, {pre, "pkg@v1.2.3", ""},
	} {
		v, ok := tc.f.TagVersion(tc.tag)
		got := ""
		if ok {
			got = v.String()
		}
		if got != tc.want {
			t.Errorf("%s.TagVersion(%q) = %q, want %q", tc.f, tc.tag, got, tc.want)
		}
	}
	a, _ := ParseVersion("0.99.2")
	b, _ := ParseVersion("1.0.0")
	c, _ := ParseVersion("0.100.0")
	if a.Compare(b) >= 0 || c.Compare(a) <= 0 || b.Compare(c) <= 0 {
		t.Error("semver precedence is not numeric per field, or does not cross a major version")
	}
}
