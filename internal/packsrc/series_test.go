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
		{"a file with no diff past the first", func(t *testing.T, pack string) {
			writeTestFile(t, filepath.Join(pack, "patches", "0003-empty.patch"),
				"From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n"+
					"Subject: [PATCH 3/3] empty\n\n")
		}, "patches", []string{"0003-empty.patch", "carries no diff", "remove it"}},
		{"a cover letter and no patch", func(t *testing.T, pack string) {
			for _, n := range patchNames(t, pack) {
				os.Remove(filepath.Join(pack, "patches", n))
			}
			writeTestFile(t, filepath.Join(pack, "patches", "0000-cover-letter.patch"), coverLetter(strings.Repeat("a", 40)))
		}, "patches", []string{"holds a cover letter and no patch", "git format-patch --base"}},
		{"a cover letter naming another base", func(t *testing.T, pack string) {
			writeTestFile(t, filepath.Join(pack, "patches", "0000-cover-letter.patch"), coverLetter(strings.Repeat("a", 40)))
		}, "patches", []string{"0000-cover-letter.patch", "a series has one base", "--base"}},
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

// coverLetter is a cover letter as `git format-patch --cover-letter --base=<base>` writes one.
func coverLetter(base string) string {
	return "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n" +
		"Subject: [PATCH 0/2] *** SUBJECT HERE ***\n\n*** BLURB HERE ***\n\n" +
		" f.txt | 2 +-\n\nbase-commit: " + base + "\n-- \n2.55.0\n"
}

// A SERIES EXPORTED WITH --cover-letter --base, the common format-patch habit, reads as-is: the
// cover letter, which alone carries the base line, gives the base and is no member; it is in the
// digest, since the base it names decides the replay; and the series replays (PF-D30).
func TestReadSeriesTakesTheBaseFromACoverLetter(t *testing.T) {
	u := newPatchedUpstream(t)
	plain := u.series(t)
	for _, version := range []string{"", "-v2"} {
		t.Run("format-patch"+version, func(t *testing.T) {
			gitIn(t, u.repo, "checkout", "-q", "-b", "cover"+version)
			for _, m := range plain.Members {
				gitIn(t, u.repo, "am", "-q", filepath.Join(u.pack, "patches", m.Name))
			}
			dir := filepath.Join(u.pack, "cover"+version)
			args := []string{"format-patch", "-q", "--cover-letter", "--base=" + u.base, "-o", dir}
			if version != "" {
				args = append(args, version)
			}
			// am needs the branch at the base, which main is.
			gitIn(t, u.repo, append(args, "main..cover"+version)...)
			gitIn(t, u.repo, "checkout", "-q", "main")
			s, err := ReadSeries(u.pack, "cover"+version)
			if err != nil {
				t.Fatalf("ReadSeries over a --cover-letter export: %v", err)
			}
			if s.Cover == nil || !strings.HasSuffix(s.Cover.Name, "0000-cover-letter.patch") || len(s.Members) != 2 {
				t.Fatalf("series = cover %v, %d members; want the cover letter apart and the two patches", s.Cover, len(s.Members))
			}
			if strings.Contains(string(s.Members[0].Data), "base-commit:") {
				t.Fatal("the fixture's first patch carries the base line itself; the cover-letter case is not exercised")
			}
			if s.Base != u.base {
				t.Errorf("base = %s, want %s from the cover letter", s.Base, u.base)
			}
			if s.Digest == SeriesDigest(s.Members) {
				t.Error("the digest leaves the cover letter, and the base it names, out")
			}
			res := u.check(t, PatchedWant{Owner: "forkpack/tool", Source: u.source("main"), Base: s.Base}, true)
			w := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", s, res.Record.Check.List, WalkOptions{})
			if w.Err != nil || w.Base != nil || w.Fit != 0 {
				t.Errorf("the --cover-letter series does not replay: %+v (base %v)", w, w.Base)
			}
		})
	}
}

// A MEMBER THAT IS AN IN-PACK LINK is refused by the read itself, not only by the listing: os.Root
// follows a link that stays inside it, so readMember checks the name again once the file is open.
func TestReadMemberRefusesAnInPackLink(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "b.txt"), "target bytes\n")
	if err := os.Symlink("b.txt", filepath.Join(dir, "a.patch")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, err := readMember(root, "a.patch", MaxSeriesBytes)
	var se *SeriesError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("readMember over an in-pack link = %q, %v; want it refused", data, err)
	}
	if _, err := readMember(root, "b.txt", MaxSeriesBytes); err != nil {
		t.Errorf("readMember over a regular file: %v", err)
	}
}
