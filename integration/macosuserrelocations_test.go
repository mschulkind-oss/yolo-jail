package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// CACHE RELOCATIONS ON A REAL macos-user LAUNCH (docs/plans/cache-relocation.md, the macos-user
// section): a user-scope `cache_relocations` entry is a link the bootstrap lays at the sandbox
// home's ~/.cache/<subdir> to the target, the Seatbelt profile opens the target, and the launch
// asks — as the sandbox account, before the nix build, and once more with a real write under the
// session profile — whether the sandbox can use it. All behind requireMacosUser, so they run on
// the scheduled macos-user.yml job and skip everywhere else.
//
// ⚠ NONE OF THIS HAS RUN YET. It was written on Linux, against code whose every decision is
// unit-tested there (internal/macosuser/relocations_test.go, internal/cli/run's
// macosuserrelocations_test.go, internal/entrypoint's darwinhomelayout_test.go); what the kernel
// and the volume answer is what the first scheduled run will say.

// macosUserRelocationVolumesEnv names the volumes macos-user.yml attaches for the /Volumes
// measurement, colon-separated: one with ownership on and one with it off. Unset everywhere else.
const macosUserRelocationVolumesEnv = "YOLO_TEST_MACOS_USER_RELOCATION_VOLUMES"

// macosUserRelocationTarget is a relocation target that does not exist yet, in a 0755 folder of
// the test's own under parent, so the launch makes it and grants it the sandbox's access. The
// folder is removed when the test ends, with sudo when the sandbox wrote into it, for
// removeMacosUserWorkspace's reason. It returns the resolved target.
func macosUserRelocationTarget(t *testing.T, parent string) string {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "yolo-it-reloc-")
	if err != nil {
		t.Fatalf("creating a relocation folder under %s: %v", parent, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// MkdirTemp makes it 0700, which the sandbox account cannot traverse; a folder a human makes
	// for a cache is 0755.
	if err := os.Chmod(resolved, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(resolved); err == nil {
			return
		}
		if !strings.HasPrefix(filepath.Base(resolved), "yolo-it-reloc-") || strings.Contains(resolved, "..") {
			t.Fatalf("refusing to escalate removal of %s: not a fixture", resolved)
		}
		if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", resolved) {
			t.Logf("could not remove %s even with sudo; remove it by hand", resolved)
		}
	})
	return filepath.Join(resolved, "cache")
}

// WHAT THE SANDBOX CACHES THERE STAYS YOURS. A launch whose relocation target yolo makes under
// /Users/Shared: the sandbox writes a nested directory through ~/.cache/<subdir>, which lands at
// the target, owned by the sandbox account — and the invoking user deletes it without sudo,
// because the target was granted the shared root's inheriting entries when it was made
// (macosuser.CacheRelocationACECommands). Without them a directory the sandbox made is one only
// sudo can remove (removeMacosUserWorkspace's reason).
func TestMacosUserCacheRelocationIsWrittenThereAndStaysYours(t *testing.T) {
	requireMacosUser(t)
	target := macosUserRelocationTarget(t, sharedUsersDir)
	const subdir = "yolo-it-reloc-own"
	packHome(t, fmt.Sprintf(`{"cache_relocations": {%q: %q}}`, subdir, target))
	ws := macosUserWorkspace(t, `{}`)

	in := "~/.cache/" + subdir
	r := runMacosUser(t, ws, strings.Join([]string{
		`echo "=== LINK ==="; readlink ` + in,
		`echo "=== WRITE ==="; mkdir -p ` + in + `/nested && echo cached > ` + in + `/nested/blob && echo WROTE || echo WRITE-FAILED`,
		`echo "=== END ==="`,
	}, "\n"))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("the launch did not run its probe (rc %d).\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== LINK ===", "=== WRITE ===")); got != target {
		t.Errorf("~/.cache/%s in the sandbox links to %q, want %s", subdir, got, target)
	}
	if !strings.Contains(section(r.stdout, "=== WRITE ===", "=== END ==="), "WROTE") {
		t.Fatalf("the sandbox could not write through ~/.cache/%s:\n%s", subdir, r.combined())
	}
	blob := filepath.Join(target, "nested", "blob")
	if b, err := os.ReadFile(blob); err != nil || strings.TrimSpace(string(b)) != "cached" {
		t.Fatalf("the sandbox's write is not at the target %s: %q (%v)", blob, b, err)
	}
	if err := os.RemoveAll(filepath.Join(target, "nested")); err != nil {
		t.Errorf("you cannot delete what the sandbox cached in the target you relocated to (%v): "+
			"the inheriting entries yolo grants a target it makes did not reach the sandbox's "+
			"directory. `ls -le %s` shows the entries.", err, target)
	}
	if !strings.Contains(r.combined(), "Cache relocation: ~/.cache/"+subdir+" → "+target) {
		t.Errorf("the relocation was not disclosed on the launch stream:\n%s", r.combined())
	}
}

// A POPULATED TARGET WITHOUT THE SANDBOX'S ACCESS REFUSES, before the nix build, naming
// `yolo macos-fix-permissions` for it: yolo grants only a target it makes, and a folder of yours
// that already holds files is never changed behind your back.
func TestMacosUserCacheRelocationRefusesAPopulatedTargetWithoutAccess(t *testing.T) {
	requireMacosUser(t)
	target := macosUserRelocationTarget(t, sharedUsersDir)
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "already-here"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const subdir = "yolo-it-reloc-closed"
	packHome(t, fmt.Sprintf(`{"cache_relocations": {%q: %q}}`, subdir, target))
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, `echo "=== END ==="`)
	out := r.combined()
	if r.rc == 0 || strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("a populated target %s the sandbox account cannot write was launched with (rc %d):\n%s",
			target, r.rc, out)
	}
	for _, want := range []string{"cannot use every cache_relocations target",
		macosuser.SandboxUser + " cannot write it", "yolo macos-fix-permissions " + target} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Building the sandbox's tools with nix") {
		t.Errorf("the refusal came after the nix build; the preflight runs before it:\n%s", out)
	}
}

// THE /Volumes MEASUREMENT (CX-D5 narrowed for this key; OQ-CX8 in
// docs/design/context-mounts.md): macos-user.yml attaches two APFS disk images under /Volumes, one
// with ownership on and one with it off, and names them in macosUserRelocationVolumesEnv. For
// each, a launch relocates a subdir to a folder on it and writes there. RECORDED, never asserted:
// the launch either writes (the profile's re-allow of the target, the probe and the volume let it
// through) or refuses with the relocation probe's message (the probe did its job: the volume is
// one the sandbox cannot write, and the user is told so before the agent). Anything else — a
// launch that ran and could not write — is a probe that let a broken target through, and fails.
func TestMacosUserCacheRelocationOnAVolumeMeasurement(t *testing.T) {
	requireMacosUser(t)
	vols := os.Getenv(macosUserRelocationVolumesEnv)
	if vols == "" {
		t.Skipf("%s is unset: no volume was attached for this measurement (macos-user.yml's "+
			"\"Attach two APFS volumes\" step sets it)", macosUserRelocationVolumesEnv)
	}
	for _, vol := range strings.Split(vols, ":") {
		t.Run(filepath.Base(vol), func(t *testing.T) {
			if st, err := os.Stat(vol); err != nil || !st.IsDir() {
				t.Skipf("the volume %s is not mounted (%v)", vol, err)
			}
			target := macosUserRelocationTarget(t, vol)
			subdir := "yolo-it-reloc-vol-" + acParityNonce()
			packHome(t, fmt.Sprintf(`{"cache_relocations": {%q: %q}}`, subdir, target))
			ws := macosUserWorkspace(t, `{}`)
			r := runMacosUser(t, ws, strings.Join([]string{
				`echo "=== WRITE ==="; echo cached > ~/.cache/` + subdir + `/blob && echo WROTE || echo WRITE-FAILED`,
				`echo "=== END ==="`,
			}, "\n"))
			out := r.combined()
			switch {
			case r.rc == 0 && strings.Contains(section(r.stdout, "=== WRITE ===", "=== END ==="), "WROTE"):
				_, err := os.Stat(filepath.Join(target, "blob"))
				t.Logf("MEASUREMENT (CX-D5, OQ-CX8; cache_relocations on %s): DELIVERED — the "+
					"sandbox wrote through the link, and the file is at the target (stat: %v).", vol, err)
			case strings.Contains(out, "cannot use every cache_relocations target"):
				t.Logf("MEASUREMENT (CX-D5, OQ-CX8; cache_relocations on %s): REFUSED before the "+
					"agent, by the relocation probes:\n%s", vol, out)
			default:
				t.Errorf("on %s the launch neither wrote through the relocation nor refused it with "+
					"the probes' message (rc %d): a target the sandbox cannot use reached the agent.\n%s",
					vol, r.rc, out)
			}
		})
	}
}
