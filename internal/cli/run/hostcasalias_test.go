package run

// hostcasalias_test.go covers L9 — the host-CAS alias (OQ-BF10) — on the run
// side: the argv the assembler emits, the pipeline wiring that decides it, the
// disclosure, and the mountpoint provisioning.
//
// internal/hostcas/hostcas_test.go covers the DECISION (every gate, every
// degenerate input). Nothing there notices `hostCASAliasArgs(in.hostCASAlias)`
// being deleted from podmanBaseMounts, or `o.planHostCASAlias(rt)` being deleted
// from runContainer — both silent: the feature just stops existing and the whole
// unit gate stays green. That is the shape AGENTS.md names, so the call sites are
// pinned here: the argv ones by assembling an argv, the pipeline one by the AST
// pin autoreapcallsite_test.go established for call sites a unit test cannot
// reach.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
)

// aliasFixture is one aliased disposition with paths that cannot be confused
// with anything else on the argv.
func aliasFixture() []hostcas.Disposition {
	return []hostcas.Disposition{{
		Store:    hostcas.Store{Name: "pants-lmdb-store", Tool: "pants", CacheRel: "pants/lmdb_store"},
		Aliased:  true,
		Source:   "/host-home/.cache/pants/lmdb_store",
		Dest:     "/home/agent/.cache/pants/lmdb_store",
		Stranded: "/yolo-state/cache/pants/lmdb_store",
		Code:     hostcas.CodeAliased,
	}}
}

func aliasInput(t *testing.T, rt string, ds []hostcas.Disposition) *assembleInput {
	t.Helper()
	in := relocationInput(t, rt, "/ws/.yolo/home", nil)
	in.hostCASAlias = ds
	return in
}

// THE CALL-SITE PIN for the argv. Delete the hostCASAliasArgs append from
// podmanBaseMounts and this is the test that goes red.
func TestPodmanArgvEmitsTheHostCASAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	argv := o.assembleRunCmd(aliasInput(t, "podman", aliasFixture()))
	want := "/host-home/.cache/pants/lmdb_store:/home/agent/.cache/pants/lmdb_store"
	if !hasMount(argv, want) {
		t.Fatalf("alias mount %q missing from the podman argv; cache mounts were %v",
			want, cacheRelocationMounts(argv))
	}
	// WRITABLE, and that is the whole trust step: a `:ro` suffix here would make
	// the alias useless (a cache the jail cannot write is a cache it cannot use)
	// and would do it silently, since podman accepts the flag either way.
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.HasPrefix(argv[i+1], "/host-home/") {
			if strings.HasSuffix(argv[i+1], ":ro") || strings.Contains(argv[i+1], ":ro,") {
				t.Errorf("alias mount %q is read-only — the alias must be writable", argv[i+1])
			}
		}
	}
}

// A DECLINED disposition must contribute no mount. The list carries every
// recognised store so the disclosure can explain a decline, so an emitter that
// forgot to filter would mount a store yolo just refused.
func TestDeclinedDispositionsEmitNoMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	declined := aliasFixture()
	declined[0].Aliased = false
	declined[0].Code = hostcas.CodeAbsent
	declined[0].Reason = "this host has no pants store"

	argv := o.assembleRunCmd(aliasInput(t, "podman", declined))
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.HasPrefix(argv[i+1], "/host-home/") {
			t.Fatalf("declined store still mounted: %q", argv[i+1])
		}
	}
}

// Apple Container must never carry an alias mount. hostcas refuses every
// non-podman runtime, so this can only regress by someone adding the emitter to
// appleContainerBaseMounts — which is precisely why the assertion is against the
// ARGV rather than against the decision.
func TestAppleContainerArgvNeverCarriesAnAliasMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	argv := o.assembleRunCmd(aliasInput(t, "container", aliasFixture()))
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.HasPrefix(argv[i+1], "/host-home/") {
			t.Fatalf("Apple Container argv carries an alias mount: %q", argv[i+1])
		}
	}
}

// The alias destination has to nest INSIDE the paths.GlobalCache() →
// /home/agent/.cache bind, or the mount lands on a path the jail's own cache
// mount then shadows and the feature is silently inert. hostcas owns the
// container-side prefix; this compares it against the parent mount the assembler
// actually emits, so the two cannot drift.
func TestAliasDestinationNestsInsideTheCacheMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	argv := o.assembleRunCmd(aliasInput(t, "podman", aliasFixture()))
	var cacheDest string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		parts := strings.Split(argv[i+1], ":")
		if len(parts) == 2 && strings.HasSuffix(parts[0], "/cache") && parts[1] == "/home/agent/.cache" {
			cacheDest = parts[1]
		}
	}
	if cacheDest == "" {
		t.Fatal("no parent cache mount on the argv — the fixture is not exercising the nesting")
	}
	dest := hostcas.Stores[0].Dest()
	if !strings.HasPrefix(dest, cacheDest+"/") {
		t.Errorf("hostcas destination %q does not nest inside the assembler's cache mount %q",
			dest, cacheDest)
	}
}

// With no aliases the argv must be byte-identical to the frozen golden: a
// machine with no recognised host store pays nothing for this feature, and the
// launcher's default probe must not be able to change that.
func TestNoAliasArgvMatchesGolden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(aliasInput(t, "podman", nil))
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with no alias:\ngot:  %v\nwant: %v",
			got, podmanLinuxGolden(home))
	}
}

// planHostCASAlias must read the environment through Options.Getenv, not
// os.Getenv: a frozen argv has to stay a function of its inputs. The process env
// deliberately points at a home with NO store while the seam points at one WITH
// a store, so switching the call to os.Getenv makes this red.
func TestPlanHostCASAliasReadsTheGetenvSeam(t *testing.T) {
	// LINUX ONLY, and not because the assertion is fragile: planHostCASAlias reads
	// hostcas.HostPlatform() directly, so on darwin the host is darwin/<arch> and
	// the jail linux/<arch> and the plan correctly refuses with "platform-mismatch"
	// before any wiring runs. There is nothing to observe. The gate logic itself is
	// tested on every platform in internal/hostcas over an injected probe; this test
	// is about the WIRING (the getenv seam, the relocation pass-through), which only
	// has an observable answer where the feature can fire.
	requireLinuxHostCAS(t)
	seamHome := t.TempDir()
	processHome := t.TempDir()
	t.Setenv("HOME", processHome)

	store := filepath.Join(seamHome, ".cache", hostcas.Stores[0].CacheRel)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "data.mdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Options{
		IsMacOS: false,
		Getenv: func(k string) string {
			if k == "HOME" {
				return seamHome
			}
			return ""
		},
		HostCASProbe: hostcas.DefaultProbe,
	}
	ds := o.planHostCASAlias("podman", nil)
	if len(ds) != len(hostcas.Stores) {
		t.Fatalf("planHostCASAlias returned %d dispositions, want %d", len(ds), len(hostcas.Stores))
	}
	if !ds[0].Aliased {
		t.Fatalf("not aliased (code %q: %s) — the seam's HOME holds a real store, so this "+
			"reads the process environment instead", ds[0].Code, ds[0].Reason)
	}
	if !strings.HasPrefix(ds[0].Source, seamHome) {
		t.Errorf("Source = %q, want it under the seam's home %q", ds[0].Source, seamHome)
	}
}

// THE CALL-SITE PIN for the relocation gate. hostcas holds the rule; nothing
// there notices the launcher forgetting to HAND IT the user's relocations, which
// is silent and re-opens the whole failure: the alias would put a relocated
// cache's biggest subtree back under the home directory the user moved it off.
func TestPlanHostCASAliasPassesTheUsersRelocationsThrough(t *testing.T) {
	// LINUX ONLY, and not because the assertion is fragile: planHostCASAlias reads
	// hostcas.HostPlatform() directly, so on darwin the host is darwin/<arch> and
	// the jail linux/<arch> and the plan correctly refuses with "platform-mismatch"
	// before any wiring runs. There is nothing to observe. The gate logic itself is
	// tested on every platform in internal/hostcas over an injected probe; this test
	// is about the WIRING (the getenv seam, the relocation pass-through), which only
	// has an observable answer where the feature can fire.
	requireLinuxHostCAS(t)
	seamHome := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	store := filepath.Join(seamHome, ".cache", hostcas.Stores[0].CacheRel)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "data.mdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{
		Getenv: func(k string) string {
			if k == "HOME" {
				return seamHome
			}
			return ""
		},
		HostCASProbe: hostcas.DefaultProbe,
	}
	seg := hostcas.Stores[0].CacheRel
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}

	if ds := o.planHostCASAlias("podman", nil); !ds[0].Aliased {
		t.Fatalf("baseline is not aliased (%q: %s) — the relocation case below would "+
			"pass for the wrong reason", ds[0].Code, ds[0].Reason)
	}
	ds := o.planHostCASAlias("podman", []config.CacheRelocation{
		{Subdir: seg, Target: "/data/relocated/" + seg},
	})
	if ds[0].Aliased {
		t.Error("aliased a store the user relocated — the launcher is not passing " +
			"cache_relocations into the plan")
	}
	if ds[0].Code != hostcas.CodeRelocated {
		t.Errorf("code = %q, want %q", ds[0].Code, hostcas.CodeRelocated)
	}
}

// The launcher's own default is the real probe, so a machine WITH a host store
// gets the alias without anything else being configured.
//
// TO BE ACCURATE ABOUT WHAT THIS CATCHES: hostcas.Plan defaults a nil probe to
// DefaultProbe itself, so losing this line is not a functional regression — the
// feature still fires. What it pins is the launcher's own convention, that every
// seam on Options is non-nil after fillDefaults, which is what lets every other
// call site read a seam without defending against nil. The stronger claim ("the
// feature would never fire") was written into an earlier commit message for this
// line and is wrong; it is corrected here rather than left in the log alone.
func TestFillDefaultsWiresTheRealProbe(t *testing.T) {
	o := &Options{}
	fillDefaults(o)
	if o.HostCASProbe == nil {
		t.Fatal("HostCASProbe is nil after fillDefaults — the decision would panic or, worse, " +
			"be silently skipped")
	}
	dir := t.TempDir()
	p := o.HostCASProbe(dir)

	// TWO PLATFORMS, ONE CONTRACT. This used to assert only the Linux answer and so
	// failed check-macos: hostcas.DefaultProbe is deliberately a stub off Linux
	// (probe_other.go returns the zero Presence) because the whole feature is
	// never-macOS. Asserting BOTH halves is better than skipping — the darwin half
	// pins the never-macOS guarantee at the one place it is implemented, so a probe
	// that started answering there would be caught rather than silently enabling an
	// alias on a backend that must not have one.
	if runtime.GOOS == "linux" {
		if !p.Exists || !p.IsDir {
			t.Errorf("default probe on a real temp dir = %+v, want an existing directory", p)
		}
		if !p.Empty {
			t.Error("default probe reports a fresh temp dir as non-empty — the cold-start gate " +
				"reads this field")
		}
		return
	}
	if p != (hostcas.Presence{}) {
		t.Errorf("off Linux the default probe must answer nothing (%+v) — the alias is "+
			"never-macOS, and a probe that reports presence there would let the gate fire "+
			"on a platform the design refuses", p)
	}
}

// requireLinuxHostCAS skips a test whose subject cannot exist off Linux. Named
// rather than inlined so `rg requireLinuxHostCAS` finds every such test at once.
func requireLinuxHostCAS(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("the host-CAS alias is never-macOS by design and planHostCASAlias reads the "+
			"real host platform, so on %s it refuses before any wiring runs", runtime.GOOS)
	}
}

// THE DISCLOSURE. An active alias always prints (a writable bind of the host
// user's cache is host access, and yolo discloses effective host access at every
// launch); a decline whose host store EXISTS prints (§5.4: a degradation is never
// silent); an absent host store prints NOTHING (§5.3: an empty store is silent —
// restating the absence of a pants cache on every launch of every jail would be
// noise, and §5.1 records what launch noise costs).
func TestNoteHostCASAliasDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mut     func(d *hostcas.Disposition)
		want    []string
		notWant []string
	}{{
		name: "active alias is disclosed with both paths",
		mut:  func(*hostcas.Disposition) {},
		want: []string{"/host-home/.cache/pants/lmdb_store", "/home/agent/.cache/pants/lmdb_store",
			"writable", "pants"},
	}, {
		name: "unwritable host store is a stated degradation",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeUnwritable
			d.Reason = "not writable by the launching user"
		},
		want:    []string{"Not aliasing", "not writable", "/yolo-state/cache/pants/lmdb_store"},
		notWant: []string{"writable): /host-home"},
	}, {
		name: "cold-start refusal is a stated degradation",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeWouldColdStart
			d.Reason = "the host store is empty while this jail's own copy is not"
		},
		want: []string{"Not aliasing", "empty"},
	}, {
		name: "absent host store is silent",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeAbsent
			d.Reason = "this host has no pants store"
		},
		notWant: []string{"pants", "alias", "Alias"},
	}, {
		name: "a non-podman backend is silent",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeBackend
			d.Reason = "it needs podman"
		},
		notWant: []string{"pants"},
	}, {
		name: "a macOS host is silent",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeMacOS
			d.Reason = "never on macOS"
		},
		notWant: []string{"pants"},
	}, {
		// The only silent decline the USER caused. Honoring cache_relocations is
		// the expected outcome, not a loss to report, and a line on every launch
		// forever would be noise about a decision they made on purpose.
		name: "a relocated cache is silent",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeRelocated
			d.Reason = "cache_relocations moves pants to storage you chose"
		},
		notWant: []string{"pants", "Not aliasing"},
	}, {
		// A mountpoint that could not be made IS actionable, and prepareHostCASAlias
		// reports it with this Code.
		name: "an unmakeable mountpoint is a stated degradation",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeUnwritable
			d.Reason = "the mountpoint could not be created"
		},
		want: []string{"Not aliasing", "could not be created"},
	}, {
		name: "a host path that is not a directory is a stated degradation",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeNotDir
			d.Reason = "exists but is not a directory"
		},
		want: []string{"Not aliasing", "not a directory"},
	}, {
		name: "no resolvable cache root is silent",
		mut: func(d *hostcas.Disposition) {
			d.Aliased = false
			d.Code = hostcas.CodeNoCacheRoot
			d.Reason = "no cache directory"
		},
		notWant: []string{"pants"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ds := aliasFixture()
			tc.mut(&ds[0])
			var buf bytes.Buffer
			o := &Options{Stderr: &buf}
			fillDefaults(o)
			o.Stderr = &buf
			o.noteHostCASAlias(ds)
			got := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("disclosure missing %q\ngot: %q", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("disclosure unexpectedly contains %q\ngot: %q", w, got)
				}
			}
			if len(tc.want) == 0 && strings.TrimSpace(got) != "" {
				t.Errorf("expected silence, got %q", got)
			}
		})
	}
}

// prepareHostCASAlias creates the mountpoint — which IS the stranded private
// copy's directory, since the alias mounts over it in place.
func TestPrepareHostCASAliasCreatesTheMountpoint(t *testing.T) {
	root := t.TempDir()
	ds := aliasFixture()
	ds[0].Stranded = filepath.Join(root, "cache", "pants", "lmdb_store")

	got := prepareHostCASAlias(ds)
	if len(got) != 1 || !got[0].Aliased {
		t.Fatalf("got %+v, want the alias to survive provisioning", got)
	}
	st, err := os.Stat(ds[0].Stranded)
	if err != nil || !st.IsDir() {
		t.Fatalf("mountpoint %q was not created: %v", ds[0].Stranded, err)
	}
}

// A mountpoint that cannot be created DOWNGRADES the disposition rather than
// failing the launch or, worse, leaving an argv naming a destination whose
// backing is missing (podman kills the container with a bare statfs error).
func TestPrepareHostCASAliasDowngradesWhenTheMountpointCannotBeMade(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "cache")
	if err := os.WriteFile(blocker, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	ds := aliasFixture()
	ds[0].Stranded = filepath.Join(blocker, "pants", "lmdb_store")

	got := prepareHostCASAlias(ds)
	if len(got) != 1 {
		t.Fatalf("got %d dispositions, want the entry kept so the decline can be explained", len(got))
	}
	if got[0].Aliased {
		t.Error("still aliased — the argv would name a destination with no mountpoint")
	}
	if !strings.Contains(got[0].Reason, "could not be created") {
		t.Errorf("Reason = %q, want it to name the provisioning failure", got[0].Reason)
	}
}

// prepareHostCASAlias must not touch a DECLINED entry's paths: hostcas declines
// on absence precisely so that a missing host store is not created, and creating
// the mountpoint for a store nothing will mount would leave an empty stub in the
// cache that reads like lost data (the reason the relocations are not provisioned
// on Apple Container either).
func TestPrepareHostCASAliasProvisionsNothingForADecline(t *testing.T) {
	root := t.TempDir()
	ds := aliasFixture()
	ds[0].Aliased = false
	ds[0].Code = hostcas.CodeAbsent
	ds[0].Stranded = filepath.Join(root, "cache", "pants", "lmdb_store")

	prepareHostCASAlias(ds)
	if _, err := os.Stat(ds[0].Stranded); !os.IsNotExist(err) {
		t.Errorf("declined entry provisioned %q anyway (err=%v)", ds[0].Stranded, err)
	}
}

// THE PIPELINE CALL-SITE PIN. Nothing above notices runContainer losing the
// plan, the provisioning, the assembleInput field or the disclosure — each is
// silent, and three of the four leave the whole unit gate green. The AST pin is
// this repo's existing answer for a call site a unit test cannot reach
// (autoreapcallsite_test.go).
//
// ORDER IS LOAD-BEARING for two of them: the plan and its provisioning must
// precede assembleRunCmd, because a bind whose mountpoint does not exist yet gets
// a root-owned one invented for it by the OCI runtime.
func TestRunContainerWiresTheHostCASAlias(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}

	calls := map[string]token.Pos{}
	fieldSet := false
	planArgs := []string(nil)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "runContainer" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fun := node.Fun.(type) {
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				case *ast.Ident:
					name = fun.Name
				}
				if _, seen := calls[name]; !seen && name != "" {
					calls[name] = node.Pos()
					if name == "planHostCASAlias" {
						for _, a := range node.Args {
							if id, ok := a.(*ast.Ident); ok {
								planArgs = append(planArgs, id.Name)
							} else {
								planArgs = append(planArgs, "<expr>")
							}
						}
					}
				}
			case *ast.KeyValueExpr:
				if k, ok := node.Key.(*ast.Ident); ok && k.Name == "hostCASAlias" {
					fieldSet = true
				}
			}
			return true
		})
	}

	for _, want := range []string{"planHostCASAlias", "prepareHostCASAlias", "noteHostCASAlias", "assembleRunCmd"} {
		if calls[want] == token.NoPos {
			t.Fatalf("runContainer does not call %s — the alias is unreachable from a real "+
				"launch and every test above still passes", want)
		}
	}
	if !fieldSet {
		t.Error("runContainer's assembleInput literal has no hostCASAlias field — the plan is " +
			"computed and then thrown away, which no other test can see")
	}
	if calls["planHostCASAlias"] > calls["assembleRunCmd"] {
		t.Error("planHostCASAlias runs AFTER assembleRunCmd — the argv would be assembled from " +
			"a plan that does not exist yet")
	}
	// THE ARGUMENTS, not just the call. Passing nil for the relocations compiles,
	// leaves every other assertion here green, and silently re-opens the failure
	// the relocation gate exists to close: the alias would put a relocated cache's
	// biggest subtree back under the home directory the user moved it off.
	if len(planArgs) != 2 || planArgs[0] != "rt" || planArgs[1] != "relocations" {
		t.Errorf("runContainer calls planHostCASAlias%v, want (rt, relocations) — the "+
			"user's cache_relocations must reach the gate that honors them", planArgs)
	}
	if calls["prepareHostCASAlias"] > calls["assembleRunCmd"] {
		t.Error("prepareHostCASAlias runs AFTER assembleRunCmd — podman kills the container " +
			"with a bare statfs error when a bind's mountpoint is missing")
	}
}

// hasMount reports whether argv carries `-v spec`, matching on the FLAG so an
// env value that happens to contain the same string cannot satisfy it.
func hasMount(argv []string, spec string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && argv[i+1] == spec {
			return true
		}
	}
	return false
}

// hostcas.JailPlatform and containerJailPlatform are two spellings of one fact —
// what the jail builds for — with different consumers (L9's platform gate and
// the capture manifest). Pinned together so neither can drift alone; the day they
// disagree, one of them is wrong about the machine.
func TestJailPlatformMatchesTheCaptureManifestsPlatform(t *testing.T) {
	if got, want := hostcas.JailPlatform(), containerJailPlatform(); got != want {
		t.Errorf("hostcas.JailPlatform() = %q but containerJailPlatform() = %q", got, want)
	}
}
