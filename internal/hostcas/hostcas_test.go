package hostcas

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// facts is a Facts value that PASSES every gate, so each test below can fail
// exactly one thing and see exactly one Code. Built around a temp tree so the
// happy path is real rather than stubbed.
func facts(t *testing.T) (Facts, string, string) {
	t.Helper()
	root := t.TempDir()
	hostCache := filepath.Join(root, "host-cache")
	jailCache := filepath.Join(root, "yolo-cache")
	src := filepath.Join(hostCache, Stores[0].CacheRel)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-empty host store: the cold-start gate is about emptiness, and a
	// fixture with no state in it would pass the happy-path assertions for the
	// wrong reason (the vacuous-fixture trap AGENTS.md names).
	if err := os.WriteFile(filepath.Join(src, "data.mdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Facts{
		Runtime:      "podman",
		IsMacOS:      false,
		HostPlatform: "linux/amd64",
		JailPlatform: "linux/amd64",
		// INJECTED, because DefaultProbe is deliberately a stub off Linux
		// (probe_other.go returns the zero Presence — the feature is never-macOS).
		// Plan's gate logic is pure and platform-independent, so it should be tested
		// on every platform; leaving this nil made every store read "absent" on
		// darwin and turned four gate tests into check-macos failures.
		Probe:         linuxLikeProbe,
		HostCacheRoot: hostCache,
		JailCacheHost: jailCache,
	}, hostCache, jailCache
}

// linuxLikeProbe is DefaultProbe's Linux behaviour expressed in portable Go, so a
// gate test means the same thing on every platform. It is deliberately NOT a
// hand-written fake: a probe that disagreed with the real one would let a gate
// test pass against a Presence the real prober never produces.
func linuxLikeProbe(path string) Presence {
	st, err := os.Stat(path)
	if err != nil {
		return Presence{}
	}
	p := Presence{Exists: true, IsDir: st.IsDir()}
	if !p.IsDir {
		return p
	}
	p.Writable = st.Mode().Perm()&0o300 == 0o300
	entries, err := os.ReadDir(path)
	p.Empty = err == nil && len(entries) == 0
	return p
}

func one(t *testing.T, f Facts) Disposition {
	t.Helper()
	ds := Plan(f)
	if len(ds) != len(Stores) {
		t.Fatalf("Plan returned %d dispositions, want one per recognised store (%d)", len(ds), len(Stores))
	}
	return ds[0]
}

// The happy path, so every negative test below has something to be a deviation
// from. An alias that never fires makes every gate test vacuous.
func TestPlanAliasesARecognisedWritableHostStore(t *testing.T) {
	f, hostCache, jailCache := facts(t)
	d := one(t, f)
	if !d.Aliased || d.Code != CodeAliased {
		t.Fatalf("aliased=%v code=%q reason=%q — want the happy path", d.Aliased, d.Code, d.Reason)
	}
	if want := filepath.Join(hostCache, Stores[0].CacheRel); d.Source != want {
		t.Errorf("Source = %q, want %q", d.Source, want)
	}
	if want := filepath.Join(jailCache, Stores[0].CacheRel); d.Stranded != want {
		t.Errorf("Stranded = %q, want %q — the private copy the alias replaces", d.Stranded, want)
	}
	if !strings.HasPrefix(d.Dest, jailCacheDir+"/") {
		t.Errorf("Dest = %q, want it nested inside the jail's cache mount %q", d.Dest, jailCacheDir)
	}
	if d.Reason != "" {
		t.Errorf("Reason = %q on the happy path, want empty", d.Reason)
	}
}

// One case per gate. Each mutates the passing Facts in exactly one way, so a
// deleted gate shows up as one failing row rather than as a whole red package.
func TestPlanGates(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Code
		mut  func(t *testing.T, f *Facts, hostCache, jailCache string)
	}{{
		name: "apple container",
		want: CodeBackend,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.Runtime = "container" },
	}, {
		name: "macos-user",
		want: CodeBackend,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.Runtime = "macos-user" },
	}, {
		// NEVER ON macOS, as its own rule. A podman-on-macOS launch reaches this.
		name: "macos host",
		want: CodeMacOS,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.IsMacOS = true },
	}, {
		name: "arch mismatch",
		want: CodePlatform,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.JailPlatform = "linux/arm64" },
	}, {
		name: "os mismatch",
		want: CodePlatform,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.HostPlatform = "darwin/amd64" },
	}, {
		name: "unknown platform",
		want: CodePlatform,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.HostPlatform = "" },
	}, {
		name: "no host cache root",
		want: CodeNoCacheRoot,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.HostCacheRoot = "" },
	}, {
		name: "relative host cache root",
		want: CodeNoCacheRoot,
		mut:  func(_ *testing.T, f *Facts, _, _ string) { f.HostCacheRoot = "relative/cache" },
	}, {
		// XDG_CACHE_HOME pointing at yolo's own cache: the source and the
		// destination's backing would be one directory, so there is no second copy
		// to alias away and the mount would be a directory bound over itself.
		name: "host store inside the jail's own cache",
		want: CodeSameTree,
		mut: func(t *testing.T, f *Facts, _, jailCache string) {
			f.HostCacheRoot = jailCache
			if err := os.MkdirAll(filepath.Join(jailCache, Stores[0].CacheRel), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}, {
		// AN EXPLICIT CONFIG DECISION OUTRANKS AN AUTOMATIC OPTIMISATION. Without
		// this gate both mounts apply and the deeper alias wins for its own
		// subtree, so a user who relocated `pants` to get 40 G off their home disk
		// silently gets 27 G of it back — the exact failure cache_relocations
		// exists to prevent, caused by the feature meant to save space.
		name: "the user relocated this cache subdir",
		want: CodeRelocated,
		mut: func(_ *testing.T, f *Facts, _, _ string) {
			f.RelocatedSegments = []string{"other", firstSegment(Stores[0].CacheRel)}
		},
	}, {
		name: "host store absent",
		want: CodeAbsent,
		mut: func(t *testing.T, f *Facts, hostCache, _ string) {
			if err := os.RemoveAll(filepath.Join(hostCache, Stores[0].CacheRel)); err != nil {
				t.Fatal(err)
			}
		},
	}, {
		name: "host store is a file",
		want: CodeNotDir,
		mut: func(t *testing.T, f *Facts, hostCache, _ string) {
			p := filepath.Join(hostCache, Stores[0].CacheRel)
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("not a store"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}, {
		// UNWRITABLE IS DRIVEN THROUGH THE PROBE SEAM, not through chmod: this
		// project's own suite runs as root, where access(2) always reports write
		// permission, so an unwritable directory is not constructible here. A gate
		// whose failure branch no test can reach is a gate that works until the day
		// it matters.
		name: "host store not writable",
		want: CodeUnwritable,
		mut: func(_ *testing.T, f *Facts, _, _ string) {
			f.Probe = func(string) Presence {
				return Presence{Exists: true, IsDir: true, Writable: false}
			}
		},
	}, {
		// The cold-start gate: an empty host store aliased over a warm private copy
		// hides a cache and buys nothing.
		name: "empty host store over a warm private copy",
		want: CodeWouldColdStart,
		mut: func(t *testing.T, f *Facts, hostCache, jailCache string) {
			if err := os.Remove(filepath.Join(hostCache, Stores[0].CacheRel, "data.mdb")); err != nil {
				t.Fatal(err)
			}
			priv := filepath.Join(jailCache, Stores[0].CacheRel)
			if err := os.MkdirAll(priv, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(priv, "data.mdb"), []byte("warm"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f, hostCache, jailCache := facts(t)
			tc.mut(t, &f, hostCache, jailCache)
			d := one(t, f)
			if d.Aliased {
				t.Fatalf("aliased anyway — %q must decline", tc.name)
			}
			if d.Code != tc.want {
				t.Fatalf("code = %q, want %q (reason: %q)", d.Code, tc.want, d.Reason)
			}
			if strings.TrimSpace(d.Reason) == "" {
				t.Error("no reason — a decline is disclosed, never silent (§5.4)")
			}
		})
	}
}

// The relocation gate is keyed on the store's TOP-LEVEL segment, which is the
// granularity cache_relocations works at — so an unrelated relocation must not
// suppress the alias, or one moved cache would switch the feature off wholesale.
func TestAnUnrelatedRelocationDoesNotBlockTheAlias(t *testing.T) {
	f, _, _ := facts(t)
	f.RelocatedSegments = []string{"huggingface", "uv", ""}
	d := one(t, f)
	if !d.Aliased {
		t.Fatalf("declined with %q (%s) — only a relocation of THIS store's segment "+
			"(%q) may block it", d.Code, d.Reason, firstSegment(Stores[0].CacheRel))
	}
}

func TestFirstSegment(t *testing.T) {
	for in, want := range map[string]string{
		"pants/lmdb_store": "pants", "pants": "pants", "a/b/c": "a", "": "",
	} {
		if got := firstSegment(in); got != want {
			t.Errorf("firstSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// The cold-start gate must not fire when there is nothing warm to lose: a fresh
// machine with an empty host store and no private copy SHOULD alias, so the
// first jail fills the shared store instead of a private one.
func TestEmptyHostStoreStillAliasesWhenNothingIsStranded(t *testing.T) {
	f, hostCache, _ := facts(t)
	if err := os.Remove(filepath.Join(hostCache, Stores[0].CacheRel, "data.mdb")); err != nil {
		t.Fatal(err)
	}
	d := one(t, f)
	if !d.Aliased {
		t.Fatalf("declined with code %q (%s) — an empty host store with no warm private copy "+
			"has nothing to cold-start", d.Code, d.Reason)
	}
}

// THE CAS BOUNDARY IS THE RULING, and this is the guard against it widening by
// accident. OQ-BF10: "aliased if and only if content-addressed … never
// named_caches". Every store below was evaluated and excluded with a measured
// reason recorded in Stores' own comment; a future edit that adds one has to
// delete this test to do it, which is the point.
func TestPathKeyedStoresAreNeverRecognised(t *testing.T) {
	forbidden := []string{
		"pants/named_caches", // path-keyed and path-poisoned; excluded by name in the ruling
		"npm",                // _cacache/index-v5 maps a request URL to a digest
		"go-build",           // <actionID>-a maps a writer-chosen action ID to an output ID
		"uv",                 // simple-v2* is index-keyed; environments-v2 holds absolute paths
		"pip",                // http-v2 is URL-keyed
		"nce",                // coupled to named_caches by the INTERP-INFO records pointing into it
	}
	for _, s := range Stores {
		for _, bad := range forbidden {
			if s.CacheRel == bad || strings.HasPrefix(s.CacheRel, bad+"/") {
				t.Errorf("Stores contains %q, which is path-keyed. OQ-BF10 excludes it "+
					"CATEGORICALLY, and the reason is injection rather than size: a "+
					"path-keyed store lets a jail write content the host tool reads because "+
					"of where it sits.", s.CacheRel)
			}
		}
		if strings.TrimSpace(s.Evidence) == "" {
			t.Errorf("Stores[%q] has no Evidence — the CAS property is the whole "+
				"justification and it is recorded, not assumed", s.Name)
		}
		if s.Name == "" || s.Tool == "" || s.CacheRel == "" {
			t.Errorf("Stores[%q] is incompletely declared: %+v", s.Name, s)
		}
		if filepath.IsAbs(s.CacheRel) || strings.Contains(s.CacheRel, "..") {
			t.Errorf("Stores[%q].CacheRel = %q must be a relative path inside the cache root",
				s.Name, s.CacheRel)
		}
	}
}

// FORBIDDEN BEHAVIOUR: Plan decides, it does not provision. Creating a missing
// host store would alias an empty directory over whatever the jail has — the one
// outcome strictly worse than doing nothing — so this pins that no path in the
// decision touches the disk.
func TestPlanMutatesNothing(t *testing.T) {
	f, hostCache, jailCache := facts(t)
	root := filepath.Dir(hostCache)
	before := treeOf(t, root)
	// Every branch, including the ones that stat a missing path.
	Plan(f)
	f.HostCacheRoot = filepath.Join(hostCache, "does-not-exist")
	Plan(f)
	f.HostCacheRoot = jailCache
	Plan(f)
	if after := treeOf(t, root); !equal(before, after) {
		t.Errorf("Plan changed the filesystem\nbefore: %v\nafter:  %v", before, after)
	}
}

func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CacheRoot's whole reason to exist is the seam, so the seam is what is tested:
// one implementation answering for two callers with different environments.
func TestCacheRoot(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	for _, tc := range []struct {
		name string
		in   map[string]string
		want string
	}{
		{"xdg wins", map[string]string{"XDG_CACHE_HOME": "/x/cache", "HOME": "/h"}, "/x/cache"},
		{"home fallback", map[string]string{"HOME": "/h"}, "/h/.cache"},
		{"relative xdg is ignored", map[string]string{"XDG_CACHE_HOME": "rel", "HOME": "/h"}, "/h/.cache"},
		{"nothing set", map[string]string{}, ""},
		{"relative home", map[string]string{"HOME": "rel"}, ""},
		{"trailing slash cleaned", map[string]string{"XDG_CACHE_HOME": "/x/cache/"}, "/x/cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CacheRoot(env(tc.in)); got != tc.want {
				t.Errorf("CacheRoot(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if got := CacheRoot(nil); got != "" {
		t.Errorf("CacheRoot(nil) = %q, want empty", got)
	}
}

// The jail is Linux whatever the host is, which is the fact the platform gate
// compares against. Stated as a test because the whole macOS refusal rests on
// it.
func TestJailPlatformIsAlwaysLinux(t *testing.T) {
	if !strings.HasPrefix(JailPlatform(), "linux/") {
		t.Errorf("JailPlatform() = %q, want a linux/<arch> value", JailPlatform())
	}
	if JailPlatform() == "" || HostPlatform() == "" {
		t.Error("platform helpers must never return an empty string — the gate reads " +
			"an empty value as a mismatch, which would silently disable the feature")
	}
}

func TestAliasedFiltersToTheEmittedMounts(t *testing.T) {
	ds := []Disposition{
		{Store: Store{Name: "a"}, Aliased: false},
		{Store: Store{Name: "b"}, Aliased: true},
		{Store: Store{Name: "c"}, Aliased: false},
	}
	got := Aliased(ds)
	if len(got) != 1 || got[0].Store.Name != "b" {
		t.Errorf("Aliased() = %+v, want only the aliased entry", got)
	}
	if len(Aliased(nil)) != 0 {
		t.Error("Aliased(nil) must be empty")
	}
}
