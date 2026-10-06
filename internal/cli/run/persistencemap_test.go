package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// THE PERSISTENCE MAP IS THE MOUNT PLAN, OR THE BRIEFING LIES (DS-D1,
// docs/design/durable-scratch-space.md). The line this replaced — "Home: /home/agent
// (persistent across sessions)" — was hand-written, and it was false the day the home went
// :ro. So the map is compared against the ASSEMBLED ARGV, per backend, in both directions:
//
//   - every writable mount the argv makes (a -v without :ro, every --tmpfs) must be covered
//     by a map entry, and the class the map gives it must agree with where the argv's
//     SOURCE lives — the workspace or <ws>/.yolo/home is "this workspace", the machine
//     store is "every workspace", a scratch volume of THIS launch or a tmpfs is "per
//     launch". So a writable dir added to the argv without the map fails here, and so does
//     a /tmp that stops being per-launch;
//   - every map entry must be an argv destination, so the map cannot name a dir no mount
//     makes.
//
// The inputs are the ones refreshJailBriefings hands persistenceMapFor — the config and the
// selected packs — assembled into the argv the way Run assembles them.
func TestThePersistenceMapIsTheMountPlan(t *testing.T) {
	for _, tc := range []struct {
		name, rt, ephemeral string
		// sealed is a fork's build jail (seal.go): its ~/.cache and /mise are its own
		// workspace's (sealedStores), and no pack's machine-scope directory is bound.
		sealed bool
	}{
		{"podman-volumes", "podman", "", false},
		{"podman-tmpfs", "podman", "tmpfs", false},
		{"apple-container", "container", "", false},
		{"podman-sealed", "podman", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			o.Sealed = tc.sealed
			cfg, packs := persistenceFixture(t, tc.ephemeral)
			const scratchID = "0123456789abcdef"
			in := relocationInput(t, tc.rt, "/ws/.yolo/home", nil)
			in.cfg, in.packs, in.scratchID = cfg, packs, scratchID
			in.writableHomeDirs = persistenceWritableHomeDirs(cfg, packs)
			in.durableDir = durable.ContainerJailPath
			if tc.sealed {
				// The two private stores Run hands a sealed launch (sealedStores), by their paths.
				in.sealed = true
				in.cacheDir = filepath.Join(paths.WorkspaceStateDir("/ws"), sealedCacheLeaf)
				in.miseStore = filepath.Join(paths.WorkspaceStateDir("/ws"), sealedMiseLeaf)
			}
			argv := o.assembleRunCmd(in)
			m := persistenceMapFor(tc.rt, cfg, packs, "/ws", tc.sealed)
			if m == nil {
				t.Fatalf("no persistence map for %s", tc.rt)
			}

			mounts := writableMountsOf(argv)
			if len(mounts) == 0 {
				t.Fatal("parsed no writable mounts from the argv; the parser is broken")
			}
			dests := map[string]bool{}
			for _, mt := range mounts {
				dests[mt.dest] = true
				got := persistenceClassOf(m, mt.dest)
				if got == 0 {
					t.Errorf("the argv mounts %q writable (from %q) and the persistence map does "+
						"not know it: add it to persistencemap.go, or the briefing's storage-classes "+
						"section omits it", mt.dest, mt.src)
					continue
				}
				if want, ok := expectedClassBySource(mt, in, scratchID); ok && !classAgrees(got, want) {
					t.Errorf("%q: the map says %s, but its argv source %q makes it %s",
						mt.dest, got, mt.src, want)
				}
			}
			for _, e := range m.Paths {
				if !dests[e.Path] {
					t.Errorf("the persistence map names %q (class %s), and the argv mounts nothing "+
						"there writable", e.Path, e.Class)
				}
			}

			// /tmp specifically, because it is the path the incident turned on: per launch,
			// and the briefing's "in RAM" note true of its backing.
			if got := persistenceClassOf(m, "/tmp"); got != jailcontent.PathPerLaunch {
				t.Errorf("/tmp is class %s in the map, want per-launch", got)
			}
			if wantRAM := tc.rt == "container" || tc.ephemeral == "tmpfs"; m.PerLaunchInRAM != wantRAM {
				t.Errorf("PerLaunchInRAM = %v, want %v", m.PerLaunchInRAM, wantRAM)
			}

			// THE DURABLE DIR HAS NO MOUNT OF ITS OWN (DS-D8): the path the argv exports is
			// reached through the workspace's bind, so no argv destination is it or lies
			// below it, and the map covers it with the workspace's class — the same relative
			// geometry on both sides is what keeps a `--relative-paths` worktree working.
			got, ok := envValue(argv, durable.EnvVar)
			if !ok || got != durable.ContainerJailPath {
				t.Fatalf("the argv exports %s=%q (present %v)", durable.EnvVar, got, ok)
			}
			for d := range dests {
				if d == got || strings.HasPrefix(d, got+"/") {
					t.Errorf("the argv mounts %q, giving the durable dir a mount of its own", d)
				}
			}
			if c := persistenceClassOf(m, got); c != jailcontent.PathProject {
				t.Errorf("the durable dir is class %s in the map; it must ride the workspace's bind", c)
			}
		})
	}
}

// And the map must REACH THE BRIEFING, from the production call site: the section is
// asserted in the file refreshJailBriefings writes, and every non-internal map entry must be
// named there. Deleting the Persistence field from refreshJailBriefings' BriefingInput, or
// the section's append in BriefingContent, fails this test.
func TestTheBriefingNamesEveryPathInThePersistenceMap(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			cfg, packs := persistenceFixture(t, "")
			o := goldenOptions(ws, home)
			o.Stdout = discardBuf()
			o.ensureDurableDir(rt, cfg)
			staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, rt,
				stagedPacks{packs: packs}, ioprio.Normal)
			if err != nil {
				t.Fatalf("refreshJailBriefings: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
			if err != nil {
				t.Fatalf("no briefing written: %v", err)
			}
			body := string(raw)
			section := sectionOf(body, "## Storage classes: what survives a restart")
			if section == "" {
				t.Fatalf("the %s briefing has no storage-classes section:\n%s", rt, body)
			}
			if strings.Contains(body, "persistent across sessions") {
				t.Errorf("the briefing still calls the home persistent:\n%s", body)
			}
			// THE ANSWER FIRST: the section's opening words are the durable dir the launch
			// made, by its variable and its path, and the per-workspace bullet offers it as
			// the place for work — above the home dirs, which are the agents' and tools' own.
			lead := "## Storage classes: what survives a restart\n\n" +
				"**`$" + durable.EnvVar + "`** (`" + durable.ContainerJailPath + "`) is scratch space for"
			if !strings.HasPrefix(section, lead) {
				t.Errorf("the %s section does not lead with the durable dir:\n%s", rt, section)
			}
			bullets := classBullets(section)
			if b := bullets[jailcontent.PathWorkspaceDurable]; !strings.HasPrefix(b,
				"- **Per workspace**: `$"+durable.EnvVar+"`, your scratch space; ") ||
				!strings.Contains(b, "the agents' and tools' own state and installs: never put your work there") {
				t.Errorf("the per-workspace bullet does not offer the durable dir first, or does not say "+
					"what the home dirs are for:\n%s", b)
			}
			// Every path is named as a `~/rel` or `/abs` code span IN ITS OWN CLASS'S bullet,
			// so a path rendered under the wrong lifecycle fails as surely as a missing one.
			for _, e := range persistenceMapFor(rt, cfg, packs, ws, false).Paths {
				if e.Class == jailcontent.PathInternal {
					if strings.Contains(section, spanFor(e.Path)) {
						t.Errorf("the section names the internal path %s as a place for work", e.Path)
					}
					continue
				}
				bullet, ok := bullets[e.Class]
				if !ok {
					t.Errorf("the section has no bullet for class %q, which holds %s:\n%s", e.Class, e.Path, section)
					continue
				}
				want := spanFor(e.Path)
				if e.Path == jailHome {
					want = "all of `/home/agent`"
				}
				if !strings.Contains(bullet, want) {
					t.Errorf("the %q bullet does not name %s:\n%s", e.Class, want, bullet)
				}
			}
			// The Home line points at the section, and says the true thing per backend.
			wantHome := "(mostly read-only; see **Storage classes** below)"
			if rt == "container" {
				wantHome = "(writable, and kept for this workspace; see **Storage classes** below)"
			}
			if !strings.Contains(body, "- **Home**: `/home/agent` "+wantHome) {
				t.Errorf("the %s Home line is not %q:\n%s", rt, wantHome, body)
			}

			// APPLE CONTAINER GETS ITS OWN WORDING, NOT PODMAN'S (docs/design/durable-scratch-space.md
			// §9 step 6), from the two facts this backend's map carries: the per-launch set is
			// tmpfs, which nothing of yolo's deletes and which costs the jail memory, and the
			// home is one writable per-workspace bind, so there is no read-only rest and yolo's
			// own files in it are rewritten at each launch instead. Asserted in the file the
			// production call site writes, so a map that stopped saying either fact fails here.
			podmanOnly := []string{
				"(on disk)", "yolo deletes these once the jail exits",
				"yolo deletes nothing here but some agents' old log files", "- **Read-only**:",
			}
			acOnly := []string{
				"- **Per launch** (in RAM): ",
				"Survives nothing: they are gone when the jail stops, and a restart is a new launch " +
					"with new, empty ones. Everything you put there uses this jail's memory.",
				"all of `/home/agent` outside the other classes, writable and kept in this workspace's `.yolo/home`",
				"yolo deletes nothing here but its own files (below).",
				"- **Rewritten at each launch**: the briefing and skills files, and a few files yolo " +
					"keeps in the home itself. A write to one may succeed here, and the next launch replaces it.",
			}
			present, absent := podmanOnly, acOnly
			if rt == "container" {
				present, absent = acOnly, podmanOnly
			}
			for _, want := range present {
				if !strings.Contains(section, want) {
					t.Errorf("the %s section does not say %q:\n%s", rt, want, section)
				}
			}
			for _, gone := range absent {
				if strings.Contains(section, gone) {
					t.Errorf("the %s section says %q, which is the other backend's:\n%s", rt, gone, section)
				}
			}
		})
	}
}

// macos-user mounts nothing, so it has no map and its briefing no section: the section for
// that backend is the design's §8 step 5, and a container-shaped one there would be false.
func TestMacosUserHasNoPersistenceMapYet(t *testing.T) {
	if m := persistenceMapFor("macos-user", newConfig(), nil, "/ws", false); m != nil {
		t.Errorf("macos-user got a persistence map: %+v", m)
	}
}

// persistenceFixture is the config and packs both tests use: the official claude pack (one
// writable dir and one machine-scope shared dir) and a writable_home_dirs entry, so every
// source of a writable home mount is represented.
func persistenceFixture(t *testing.T, ephemeral string) (*jsonx.OrderedMap, []*packload.Pack) {
	t.Helper()
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("security", sec, "writable_home_dirs", []any{".pi-lens"})
	if ephemeral != "" {
		cfg.Set("ephemeral_storage", ephemeral)
	}
	packs := claudePackFixture(t)
	if got := persistenceWritableHomeDirs(cfg, packs); len(got) != 1 {
		t.Fatalf("the fixture's writable_home_dirs entry did not validate: %v", got)
	}
	return cfg, packs
}

// persistenceWritableHomeDirs is what Run hands assembleInput.writableHomeDirs.
func persistenceWritableHomeDirs(cfg *jsonx.OrderedMap, packs []*packload.Pack) []string {
	return config.WritableHomeDirs(cfg, packs)
}

type writableMount struct {
	src, dest string
	tmpfs     bool
}

// writableMountsOf parses every writable mount out of an argv: `-v src:dest[:opts]` whose
// options do not include ro, and every `--tmpfs dest[:opts]`.
func writableMountsOf(argv []string) []writableMount {
	var out []writableMount
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-v", "--volume":
			parts := strings.Split(argv[i+1], ":")
			if len(parts) < 2 {
				continue
			}
			if len(parts) >= 3 && strings.Contains(","+parts[2]+",", ",ro,") {
				continue
			}
			out = append(out, writableMount{src: parts[0], dest: parts[1]})
		case "--tmpfs":
			out = append(out, writableMount{dest: strings.SplitN(argv[i+1], ":", 2)[0], tmpfs: true})
		}
	}
	return out
}

// expectedClassBySource is the class an argv mount's SOURCE implies, where the source says.
// ok is false for a source that carries no scope of its own (the per-jail host-services dir
// under /run, which is per launch by the tmpfs it sits in).
func expectedClassBySource(mt writableMount, in *assembleInput, scratchID string) (jailcontent.PathClass, bool) {
	switch {
	case mt.tmpfs:
		return jailcontent.PathPerLaunch, true
	case mt.src == "/ws":
		return jailcontent.PathProject, true
	case mt.src == in.wsState || strings.HasPrefix(mt.src, in.wsState+"/"):
		return jailcontent.PathWorkspaceDurable, true
	case strings.HasPrefix(mt.src, paths.WorkspaceStateDir("/ws")+"/"):
		// The workspace's own state dir beyond its home overlay: a sealed build's private
		// ~/.cache and /mise (sealedStores).
		return jailcontent.PathWorkspaceDurable, true
	case strings.HasPrefix(mt.src, paths.GlobalStorage()+"/"), mt.src == in.miseStore, mt.src == miseStoreVolume:
		return jailcontent.PathMachineDurable, true
	}
	// Apple Container's tool disk (OQ-MB1): this workspace's own when it carries this jail's
	// name, and not a store any workspace shares.
	if cname, ok := prune.ParseMiseVolumeName(mt.src); ok {
		if cname != in.cname {
			return 0, true
		}
		return jailcontent.PathWorkspaceDurable, true
	}
	if _, id, _, ok := prune.ParseScratchVolumeName(mt.src); ok {
		if id != scratchID {
			// A scratch volume that is not this launch's is a /tmp a relaunch could be
			// handed again: the per-launch property is gone.
			return 0, true
		}
		return jailcontent.PathPerLaunch, true
	}
	return 0, false
}

// classAgrees: a per-workspace source may back an INTERNAL entry (yolo's own files in
// <ws>/.yolo/home), and that is the only licensed difference.
func classAgrees(got, want jailcontent.PathClass) bool {
	return got == want || (want == jailcontent.PathWorkspaceDurable && got == jailcontent.PathInternal)
}

// persistenceClassOf is the class the map gives an in-jail path: the class of the deepest
// map entry at or above it, or 0 when none covers it. A mount nested inside a mapped
// directory (a cache relocation inside ~/.cache, a per-side shadow inside /workspace)
// inherits that directory's class, which is how podman resolves it: the deeper bind wins
// only for its own subtree.
func persistenceClassOf(m *jailcontent.PersistenceMap, p string) jailcontent.PathClass {
	best, bestLen := jailcontent.PathClass(0), -1
	for _, e := range m.Paths {
		if (p == e.Path || strings.HasPrefix(p, e.Path+"/")) && len(e.Path) > bestLen {
			best, bestLen = e.Class, len(e.Path)
		}
	}
	return best
}

// classBullets maps each class to the bullet line that renders it, by the bullet's label.
func classBullets(section string) map[jailcontent.PathClass]string {
	labels := map[string]jailcontent.PathClass{
		"- **Per launch**":                      jailcontent.PathPerLaunch,
		"- **Per workspace**":                   jailcontent.PathWorkspaceDurable,
		"- **Every workspace on this machine**": jailcontent.PathMachineDurable,
		"- **The workspace itself**":            jailcontent.PathProject,
	}
	out := map[jailcontent.PathClass]string{}
	for _, line := range strings.Split(section, "\n") {
		for label, c := range labels {
			if strings.HasPrefix(line, label) {
				out[c] = line
			}
		}
	}
	return out
}

// spanFor is how the section spells a path: `~/rel` under the home, `/abs` elsewhere.
func spanFor(p string) string {
	if strings.HasPrefix(p, jailHome+"/") {
		return "`~/" + strings.TrimPrefix(p, jailHome+"/") + "`"
	}
	return "`" + p + "`"
}

// sectionOf returns the `## ` section headed by heading, up to the next `## `.
func sectionOf(body, heading string) string {
	i := strings.Index(body, heading)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	if j := strings.Index(rest[len(heading):], "\n## "); j >= 0 {
		return rest[:len(heading)+j+1]
	}
	return rest
}
