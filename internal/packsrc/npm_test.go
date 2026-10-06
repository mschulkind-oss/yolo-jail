package packsrc

// npm_test.go pins an NPM SOURCE's check (npm.go; docs/design/pi-extension-store-builds.md XB-D5,
// XB-D11) and an UNMODIFIED git extension's check and walk (EmptySeries, XB-D1, XB-D2): what each
// spec resolves to and how often the registry is asked, the record it leaves, and a walk that
// replays nothing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseNpmReadsEachSpecAsNpmDoes(t *testing.T) {
	for _, c := range []struct {
		src, name, ref string
		kind           NpmSpecKind
	}{
		{"npm:pi-web-access", "pi-web-access", "latest", NpmTag},
		{"npm:pi-web-access@next", "pi-web-access", "next", NpmTag},
		{"npm:pi-web-access@1.4.2", "pi-web-access", "1.4.2", NpmVersion},
		{"npm:pi-web-access@v1.4.2", "pi-web-access", "v1.4.2", NpmVersion},
		{"npm:pi-web-access@^1.4.0", "pi-web-access", "^1.4.0", NpmRange},
		{"npm:pi-web-access@1.x", "pi-web-access", "1.x", NpmRange},
		{"npm:@org/pi-thing", "@org/pi-thing", "latest", NpmTag},
		{"npm:@org/pi-thing@>=2.0.0 <3", "@org/pi-thing", ">=2.0.0 <3", NpmRange},
	} {
		n, err := ParseNpm(c.src)
		if err != nil {
			t.Errorf("ParseNpm(%q): %v", c.src, err)
			continue
		}
		if n.Name != c.name || n.Ref() != c.ref || n.Kind != c.kind || n.Repo() != "npm:"+c.name {
			t.Errorf("ParseNpm(%q) = name %q ref %q kind %d repo %q; want %q %q %d", c.src, n.Name, n.Ref(),
				n.Kind, n.Repo(), c.name, c.ref, c.kind)
		}
	}
	for _, bad := range []string{"npm:", "npm:Upper", "npm:-x", "npm:.x", "npm:a/b", "npm:@org", "npm:x@", "npm:x@a b",
		"npm:x@ 1.2.3", "git+https://h/x?ref=main"} {
		if _, err := ParseNpm(bad); err == nil {
			t.Errorf("ParseNpm(%q) took a source npm does not name", bad)
		}
	}
	if got := BuildSource("npm:pi-web-access@^1"); got != "npm:pi-web-access" {
		t.Errorf("BuildSource of an npm source = %q, want the package without its spec", got)
	}
	if got := CheckRepo("npm:@org/x@1.0.0"); got != "npm:@org/x" {
		t.Errorf("CheckRepo of an npm source = %q", got)
	}
}

// fakeRegistry serves one package's abbreviated metadata and counts the requests it answers.
func fakeRegistry(t *testing.T, name, body string) *int64 {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if !strings.Contains(r.Header.Get("Accept"), "application/vnd.npm.install-v1+json") {
			t.Errorf("the check did not ask for the abbreviated metadata: Accept %q", r.Header.Get("Accept"))
		}
		if r.URL.EscapedPath() != "/"+strings.ReplaceAll(name, "/", "%2F") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := NpmRegistry
	NpmRegistry = srv.URL
	t.Cleanup(func() { NpmRegistry = old })
	return &hits
}

const piWebAccessMeta = `{"name":"pi-web-access","dist-tags":{"latest":"1.4.2","next":"2.0.0-beta.1"},
"versions":{"1.3.0":{},"1.4.1":{},"1.4.2":{},"1.5.0":{"deprecated":"broken, use 1.4.2"},"2.0.0-beta.1":{}}}`

func TestAnNpmCheckResolvesEachSpecAsNpmsPickManifestDoes(t *testing.T) {
	hits := fakeRegistry(t, "pi-web-access", piWebAccessMeta)
	for _, c := range []struct{ spec, want string }{
		{"", "1.4.2"},             // the latest tag
		{"@next", "2.0.0-beta.1"}, // a dist-tag
		{"@^1.3.0", "1.4.2"},      // latest satisfies the range, so latest
		{"@~1.3.0", "1.3.0"},      // the highest match below latest
		{"@>=1.4.2", "1.4.2"},     // latest satisfies it, though 1.5.0 is higher
		{"@1.5.x", "1.5.0"},       // only a deprecated version satisfies it: taken anyway
	} {
		s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
		res := s.CheckPatched(PatchedWant{Owner: "p/pi-web-access", Source: "npm:pi-web-access" + c.spec},
			CheckOptions{Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
		if res.Err != nil || !res.Ran {
			t.Fatalf("%s: CheckPatched ran %v: %v", c.spec, res.Ran, res.Err)
		}
		found := res.Record.Check
		if found.Problem != "" || len(found.List) != 1 || found.List[0].Commit != c.want ||
			found.List[0].Label() != c.want {
			t.Errorf("npm:pi-web-access%s: list %+v (problem %q), want the one version %s", c.spec, found.List,
				found.Problem, c.want)
		}
		if res.Record.Read != (CheckInputs{Repo: "npm:pi-web-access", Ref: strings.TrimPrefix(c.spec, "@")}) &&
			c.spec != "" {
			t.Errorf("%s: the record read %+v", c.spec, res.Record.Read)
		}
	}
	if got := atomic.LoadInt64(hits); got != 6 {
		t.Errorf("the registry was asked %d times for six checks, want one each", got)
	}
}

// A RANGE PICKS AS npm's PICK-MANIFEST DOES (npm 11's bundled npm-pick-manifest 11.0.3, XB-D36,
// XB-D47): the `latest` tag only when the registry lists it and it is not deprecated, and for the
// range `*` whatever it is, a pre-release included. No spec is `*`, since npm-package-arg reads
// `npm install <name>` as that range, which is the install pi runs for `npm:<name>`; an explicit
// dist-tag is that tag's version, deprecated or not, but only a version the registry lists.
func TestAnNpmRangePicksAsNpmPickManifestDoes(t *testing.T) {
	for _, c := range []struct{ name, src, body, want, wantErr string }{
		{"a deprecated latest is passed over for a range", "npm:x@^1.0.0",
			`{"dist-tags":{"latest":"1.5.0"},"versions":{"1.4.0":{},"1.5.0":{"deprecated":"use 1.4"}}}`, "1.4.0", ""},
		{"* takes a pre-release latest", "npm:x@*",
			`{"dist-tags":{"latest":"2.0.0-beta.1"},"versions":{"1.4.0":{},"2.0.0-beta.1":{}}}`, "2.0.0-beta.1", ""},
		{"no spec is *, so it takes a pre-release latest", "npm:x",
			`{"dist-tags":{"latest":"2.0.0-beta.1"},"versions":{"1.4.0":{},"2.0.0-beta.1":{}}}`, "2.0.0-beta.1", ""},
		{"no spec passes over a deprecated latest", "npm:x",
			`{"dist-tags":{"latest":"1.5.0"},"versions":{"1.4.0":{},"1.5.0":{"deprecated":"use 1.4"}}}`, "1.4.0", ""},
		{"a latest the registry does not list is passed over", "npm:x@^1.0.0",
			`{"dist-tags":{"latest":"1.6.0"},"versions":{"1.4.0":{},"1.5.0":{}}}`, "1.5.0", ""},
		{"an explicit latest is the tag's version, deprecated or not", "npm:x@latest",
			`{"dist-tags":{"latest":"1.5.0"},"versions":{"1.4.0":{},"1.5.0":{"deprecated":"use 1.4"}}}`, "1.5.0", ""},
		{"a dist-tag naming an unlisted version resolves to nothing", "npm:x@next",
			`{"dist-tags":{"next":"3.0.0"},"versions":{"1.4.0":{}}}`, "", "names 3.0.0, which the registry does not list"},
	} {
		var p npmPackument
		if err := json.Unmarshal([]byte(c.body), &p); err != nil {
			t.Fatal(err)
		}
		n, err := ParseNpm(c.src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.pick(n)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: %s picks %q (%v), want an error naming %q", c.name, c.src, got, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %s picks %q (%v), npm installs %s", c.name, c.src, got, err, c.want)
		}
	}
}

// AN EXACT VERSION NEEDS NO REQUEST AT ALL (XB-D5), and once it resolves it is never due again: what
// it names never moves (XB-D2).
func TestAnExactNpmVersionAsksNoRegistryAndIsCheckedOnce(t *testing.T) {
	hits := fakeRegistry(t, "pi-web-access", piWebAccessMeta)
	s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	now := time.Unix(1_800_000_000, 0)
	w := PatchedWant{Owner: "p/pi-web-access", Source: "npm:pi-web-access@1.3.0"}
	res := s.CheckPatched(w, CheckOptions{Now: func() time.Time { return now }})
	if res.Err != nil || !res.Ran || res.Record.Check.List[0].Commit != "1.3.0" || res.Record.Check.RefKind != RefKindNpmVersion {
		t.Fatalf("an exact version's check: ran %v, record %+v, err %v", res.Ran, res.Record.Check, res.Err)
	}
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Errorf("an exact version asked the registry %d times", got)
	}
	later := now.Add(48 * time.Hour)
	if res := s.CheckPatched(w, CheckOptions{Now: func() time.Time { return later }}); res.Ran {
		t.Error("an exact version that resolved was checked again two days later")
	}
	// A dist-tag moves, so it is checked hourly as a branch is.
	t2 := PatchedWant{Owner: "p/pi-web-access-2", Source: "npm:pi-web-access@next"}
	_ = s.CheckPatched(t2, CheckOptions{Now: func() time.Time { return now }})
	if res := s.CheckPatched(t2, CheckOptions{Now: func() time.Time { return later }}); !res.Ran {
		t.Error("a dist-tag was not checked again past the interval")
	}
}

func TestAnUnreachableRegistryIsTheChecksProblemAndNamesTheRetry(t *testing.T) {
	old := NpmRegistry
	NpmRegistry = "http://127.0.0.1:1"
	t.Cleanup(func() { NpmRegistry = old })
	s := &Store{Dir: t.TempDir(), Getenv: noStagedTree, Timeout: 2 * time.Second}
	res := s.CheckPatched(PatchedWant{Owner: "p/x", Source: "npm:x@^1"}, CheckOptions{})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	c := res.Record.Check
	if c.FetchErr == "" || !strings.Contains(c.Problem, "could not ask the registry about npm:x") ||
		!strings.Contains(c.Problem, "`yolo pack update` checks now") || len(c.List) != 0 {
		t.Errorf("an unreachable registry: %+v", c)
	}
	if res := s.CheckPatched(PatchedWant{Owner: "p/y", Source: "npm:nope@^1"}, CheckOptions{}); res.Record.Check.Problem == "" {
		t.Error("no problem for a second unreachable check")
	}
}

// A SPEC THE REGISTRY CANNOT ANSWER NAMES THE EDIT, not a wait: a package the registry does not
// have, a dist-tag it does not carry, a range nothing satisfies. The next check would get the same
// answer, so "in an hour, tries again" is no next step; the source's name or spec is, and
// `yolo pack update` checks again once it is edited. None is a failed fetch.
func TestAnNpmSpecTheRegistryCannotAnswerNamesTheEdit(t *testing.T) {
	fakeRegistry(t, "pi-web-access", piWebAccessMeta)
	for _, c := range []struct{ src, want string }{
		{"npm:pi-web-acess", "the registry has no package pi-web-acess — check the source's package name, " +
			"`npm:pi-web-acess`"},
		{"npm:pi-web-access@nightly", "check the source's spec, `npm:pi-web-access@nightly`"},
		{"npm:pi-web-access@^9.0.0", "check the source's spec, `npm:pi-web-access@^9.0.0`"},
	} {
		s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
		res := s.CheckPatched(PatchedWant{Owner: "p/x", Source: c.src}, CheckOptions{})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		found := res.Record.Check
		if !strings.Contains(found.Problem, c.want) || !strings.Contains(found.Problem, "`yolo pack update`") ||
			strings.Contains(found.Problem, "tries again") || found.FetchErr != "" || len(found.List) != 0 {
			t.Errorf("%s: problem %q, fetch error %q; want the edit %q and no wait", c.src, found.Problem,
				found.FetchErr, c.want)
		}
	}
}

func TestAnNpmListIsCutAtTheGoodBuildsVersionAlone(t *testing.T) {
	in := CheckInputs{Repo: "npm:x", Ref: "latest"}
	r := &CheckRecord{Read: in, Check: &CheckFound{RefKind: RefKindNpmTag, List: []ListEntry{NpmListEntry("1.2.0")}},
		Good: &GoodBuild{Commit: "1.2.0", Version: "1.2.0"}}
	if got := r.Candidates(in); len(got) != 0 {
		t.Errorf("the good build's own version is a candidate: %+v", got)
	}
	// The tag moved back: npm itself would install 1.1.0 now, so it is the candidate.
	r.Check.List = []ListEntry{NpmListEntry("1.1.0")}
	if got := r.Candidates(in); len(got) != 1 || got[0].Commit != "1.1.0" {
		t.Errorf("the registry's answer is not the candidate: %+v", got)
	}
}

// AN UNMODIFIED GIT EXTENSION's check lists what its follow rule names with no base to make present,
// and its walk replays nothing: every entry fits, its tree the upstream's own (XB-D1).
func TestAnUnmodifiedGitExtensionIsCheckedAndWalkedWithNoSeries(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{3: "three"})
	tip := u.release(t, "", map[int]string{3: "three", 4: "four"})
	w := PatchedWant{Owner: "p/ext", Source: u.source("main"), Follow: "head"}
	res := u.check(t, w, false)
	c := res.Record.Check
	if c.Problem != "" || c.RefKind != "branch" || c.BaseOnBranch {
		t.Fatalf("an unmodified extension's check: %+v", c)
	}
	if got := listLabels(c.List); got != shortCommit(tip)+"* v1.1.0 v1.0.0" {
		t.Errorf("the walk's list = %q, want the tip then every version", got)
	}
	var exported string
	walk := u.store.WalkSeries(mustAddr(t, u.source("main")).Repo, "", EmptySeries(), c.List, WalkOptions{OnFit: func(tree string) error {
		data, err := os.ReadFile(filepath.Join(tree, "f.txt"))
		exported = string(data)
		return err
	}})
	if walk.Err != nil || walk.Base != nil || walk.Fit != 0 || !walk.Results[0].Clean {
		t.Fatalf("the empty series' walk: %+v", walk)
	}
	if !strings.Contains(exported, "four\n") {
		t.Errorf("the fit's tree is not the tip's: %q", exported)
	}
	if EmptySeries().Digest == u.series(t).Digest || EmptySeries().Len() != 0 {
		t.Error("the empty series' digest is a patched series'")
	}
	_ = v11
	// HELD AT A TAG, it is checked only until it first resolves (XB-D2).
	held := PatchedWant{Owner: "p/held", Source: u.source("v1.1.0"), Follow: "head"}
	u.check(t, held, false)
	u.now = u.now.Add(48 * time.Hour)
	if res := u.check(t, held, false); res.Ran {
		t.Error("an unmodified extension held at a tag was checked again")
	}
	// A patched fork at a tag still is, hourly: a series names a base.
	patched := u.want(t, "v1.1.0", "")
	u.check(t, patched, false)
	u.now = u.now.Add(2 * time.Hour)
	if res := u.check(t, patched, false); !res.Ran {
		t.Error("a patched fork held at a tag is no longer checked hourly")
	}
}

func TestAnNpmWalkFitsTheRegistrysAnswerAndHandsAnEmptyCheckout(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	var saw []string
	w := s.WalkSeries("npm:x", "", EmptySeries(), []ListEntry{NpmListEntry("1.2.3")}, WalkOptions{OnFit: func(tree string) error {
		entries, err := os.ReadDir(tree)
		for _, e := range entries {
			saw = append(saw, e.Name())
		}
		return err
	}})
	if w.Fit != 0 || len(w.Results) != 1 || !w.Results[0].Clean || w.Results[0].Entry.Commit != "1.2.3" || len(saw) != 0 {
		t.Errorf("an npm walk: %+v, checkout holding %v", w, saw)
	}
}
