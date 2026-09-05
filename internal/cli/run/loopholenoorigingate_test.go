package run

// loopholenoorigingate_test.go pins OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) for the
// SHARPEST kind: a FETCHED pack's loophole, never approved, spawns its host daemon and gets
// every crossing it declares into the container argv.
//
// # What this file used to be
//
// loopholeorigingate_test.go, pinning §4.3 G3's opposite — "a fetched pack whose loophole
// claim is unapproved has its loophole NOT DISCOVERED AT ALL". The ruling deleted the gate,
// not the enforcement: nothing about how a loophole crosses changed, only who is allowed to.
//
// # Why this is end-to-end and not a unit test, which is unchanged and is the point
//
// The old defect was a gate that was CORRECT and IGNORED: HostExecApproved was computed per
// module and had one production reader, so an unapproved fetched pack's daemon ran anyway. A
// test over the predicate could not see that, so the assertion was the SENTINEL FILE the
// daemon writes and the flags in the argv.
//
// That shape is exactly what the inverse needs. A gate reintroduced anywhere between `packs`
// and the spawn — in packLoopholeModules, in packload, in stagePacks — leaves the sentinel
// unwritten and turns this red, whether or not any predicate is "correct".

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fetchedLoopholePack builds a GIT pack (git+file://, so it is genuinely OriginFetched with
// no network) whose one loophole declares a host_daemon that TOUCHES A SENTINEL, plus a bind
// mount, a device and an intercept. It installs the pack into the store the launch reads,
// writes the user config that selects it, and returns the sentinel path.
//
// git+file:// and NO LOCKFILE ENTRY: this is the exact state the deleted gate refused —
// installed, selected, never approved — so a gate reintroduced anywhere on the launch path
// finds its unapproved branch here. The pack must actually be in the store, because launch
// resolution is strictly offline.
func fetchedLoopholePack(t *testing.T, home string) (sentinel string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	sentinel = filepath.Join(t.TempDir(), "daemon-ran")
	mod := filepath.Join(repo, "loopholes", "acme-proxy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	// A daemon whose whole behaviour is observable: it writes the sentinel, then binds
	// nothing. The readiness wait will fail (nothing is published) and that is fine — the
	// question is whether the process was STARTED, which the sentinel answers regardless.
	manifest := `{"name":"acme-proxy","description":"unapproved","default_enabled":true,` +
		`"transport":"loopback-tls","lifecycle":"spawned",` +
		`"host_daemon":{"cmd":["/bin/sh","-c","touch ` + sentinel + `"],"publishes":"socket"},` +
		`"intercepts":[{"host":"api.acme.test"}],` +
		`"ca_cert":"ca.crt",` +
		`"host_bind_mounts":[{"host":"{loophole_dir}/conf","container":"/etc/acme"}],` +
		`"host_devices":["/dev/null"]}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// The CA and the bind source have to EXIST, or RuntimeArgsFor skips them for a reason
	// that has nothing to do with the gate and the argv assertion passes vacuously.
	if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("-----BEGIN CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A FILE inside the bind source, not just the dir: packstage stages files, so an empty
	// directory never lands in the staged tree and RuntimeArgsFor would skip the bind for a
	// missing source — passing the argv assertion for the wrong reason.
	if err := os.MkdirAll(filepath.Join(mod, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "conf", "acme.conf"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"},` +
		`{"kind":"env","vars":{"ACME_OTHER_CONTRIBUTION":"1"}}]}`
	if err := os.WriteFile(filepath.Join(repo, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		// CleanGitEnv first: hook-exported git state is ABSOLUTE from a linked
		// worktree and would redirect this helper onto the committer's index.
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-qm", "pack")

	src := "git+file://" + repo + "?ref=main"
	writeUserPacks(t, home, `[{"name": "acme", "source": "`+src+`"}]`)
	// Populate the store the way `yolo pack install` would, MINUS the approval — which is
	// the whole situation under test: installed, selected, never approved.
	syncPackStore(t, src)
	return sentinel
}

// syncPackStore fetches an address into the store the launch resolves from, without
// recording any approval. It is `yolo pack install`'s fetch half and nothing else.
func syncPackStore(t *testing.T, src string) {
	t.Helper()
	addr, err := packsrc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	commit, err := store.Sync(addr)
	if err != nil {
		t.Fatalf("syncing the test pack: %v", err)
	}
	if _, err := store.Materialize(addr, commit); err != nil {
		t.Fatalf("materializing the test pack: %v", err)
	}
}

// A FETCHED PACK'S LOOPHOLE SPAWNS AND CROSSES, with no approval anywhere — the inverse of
// TestUnapprovedFetchedPackLoopholeNeitherSpawnsNorReachesTheArgv, assertion for assertion.
//
// Four surfaces, because the gate governed all four and a reintroduction could pick any one:
// the LAUNCH (it must not refuse), the SPAWN (the sentinel the daemon writes), the ARGV (the
// intercept, the bind, the device and the CA), and the BRIEFING (the agent is told the
// capability is live).
func TestFetchedPackLoopholeSpawnsAndCrossesWithNoApproval(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeLoopholes(t)
	sentinel := fetchedLoopholePack(t, home)

	var out bytes.Buffer
	cname := "yolo-test-fetched-loophole"
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	o.ServiceReadyTimeout = 200 * time.Millisecond

	// 0. THE LAUNCH. A refusal here is the deleted gate, back.
	_, loaded, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("a fetched pack's loophole REFUSED THE LAUNCH:\n%v\n\nOQ-TP9 deleted that "+
			"gate — the person who put a git URL in their own user config already granted "+
			"strictly more than it withheld.\n%s", err, out.String())
	}
	if len(loaded) != 1 {
		t.Fatalf("staging returned %d packs, want the one configured pack", len(loaded))
	}
	// AND THE MODULE IS RECORDED. stagePacks calls loopholes.SetPackModules on its way out;
	// under the old gate the refusal returned first and the converged set never learned the
	// loophole existed.
	if _, ok := loopholes.NewHostSet(nil).Lookup("acme-proxy"); !ok {
		t.Fatal("the pack's loophole is missing from the converged discovery set")
	}

	// 1. THE SPAWN, through the one disclosed call site.
	o.startLoopholesDisclosed(cname, "podman", jsonx.NewOrderedMap(), loaded)
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("the fetched pack's host daemon never ran:\n%s\nNothing gates it any more, "+
			"so a missing sentinel is either a reintroduced gate or a broken spawn path",
			out.String())
	}

	// 2. THE ARGV. Every crossing the manifest declares, because the deleted gate governed the
	// whole loophole rather than just the daemon — so a partial reintroduction shows up here.
	argv := strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	for _, want := range []string{
		"api.acme.test",       // --add-host
		"/etc/acme",           // bind mount destination
		"/dev/null",           // device passthrough
		"NODE_EXTRA_CA_CERTS", // the CA
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the container argv is missing %q from the fetched pack's loophole:\n%s",
				want, argv)
		}
	}

	// 3. THE BRIEFING. The agent ACTS ON this, so a live capability the briefing omits sends
	// it to work around wiring that is actually there — the mirror of the old defect, where a
	// withheld one was advertised.
	advertised := false
	for _, lo := range briefingLoopholes(nil) {
		if lo.Name == "acme-proxy" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("the jail briefing does not advertise the fetched pack's loophole, though " +
			"its daemon ran and its binds crossed")
	}
}

// A LOCAL (file://) pack's loophole reaches the same two surfaces. It was the "approved
// origin" control, proving the gate was a gate rather than a ban; with no gate left it is a
// DELIVERY-ROUTE control instead — origin still decides how a pack's content gets to the
// store (a file:// pack needs no `pack install`), and this pins that the two routes end in
// the same place.
func TestLocalPackLoopholeReachesTheSpawnAndTheArgv(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeLoopholes(t)

	root := filepath.Join(t.TempDir(), "localpack")
	mod := filepath.Join(root, "loopholes", "acme-proxy")
	if err := os.MkdirAll(filepath.Join(mod, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "conf", "acme.conf"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "daemon-ran")
	manifest := `{"name":"acme-proxy","description":"approved","default_enabled":true,` +
		`"transport":"loopback-tls","lifecycle":"spawned",` +
		`"host_daemon":{"cmd":["/bin/sh","-c","touch ` + sentinel + `"],"publishes":"socket"},` +
		`"intercepts":[{"host":"api.acme.test"}],` +
		`"host_bind_mounts":[{"host":"{loophole_dir}/conf","container":"/etc/acme"}]}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+root+`"]`)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	o.ServiceReadyTimeout = 200 * time.Millisecond
	_, loaded, _, err := o.stagePacks("yolo-test-approved-loophole")
	if err != nil {
		t.Fatal(err)
	}
	o.startLoopholesDisclosed("yolo-test-approved-loophole", "podman", jsonx.NewOrderedMap(), loaded)
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("a local pack loophole's host daemon did not start:\n%s", out.String())
	}
	argv := strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	for _, want := range []string{"api.acme.test", "/etc/acme"} {
		if !strings.Contains(argv, want) {
			t.Errorf("a local pack loophole's %q is missing from the argv:\n%s", want, argv)
		}
	}
	// And the record IS in the converged set.
	if _, ok := loopholes.NewHostSet(nil).Lookup("acme-proxy"); !ok {
		t.Error("an approved pack loophole is missing from the converged discovery set")
	}
	// And the briefing DOES advertise it.
	advertised := false
	for _, lo := range briefingLoopholes(nil) {
		if lo.Name == "acme-proxy" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("a local pack loophole is missing from the jail briefing")
	}
}

// A MODULE NOBODY VOUCHED FOR IS STILL DISCOVERED AND LISTED, and this is the ONE refusal in
// this area that OQ-TP9 did not touch.
//
// HostExecApproved:false no longer means "a fetched pack the user did not approve" — no
// production caller passes it. It means the caller assembled records WITHOUT resolving packs
// at all, which is a programming error rather than a user's config, and internal/loopholes
// refuses to run host code for it because a plain []*Loophole carries no provenance
// (gateAdmitsCrossing). What is pinned here is that refusing to RUN it does not make it
// VANISH: a loophole missing from `yolo loopholes list` is indistinguishable from a pack that
// failed to stage, so the diagnosis has to stay visible.
func TestPackLoopholeWithNoOriginDecisionIsStillListed(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeLoopholes(t)
	mod := writeLoopholeModule(t, t.TempDir(), "acme-proxy", "")
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: false}})

	set := loopholes.NewHostSet(nil)
	lp, ok := set.Lookup("acme-proxy")
	if !ok {
		t.Fatal("a pack loophole with no origin decision vanished from the converged set — a " +
			"missing entry reads as a pack that failed to stage, which is a much worse " +
			"diagnosis than the one it deserves")
	}
	if set.MayRunHostCode(lp) {
		t.Fatal("the fixture is wrong: the record must carry no origin decision")
	}
	// Active but NOT honored — the distinction the briefing needed.
	if !lp.Active() {
		t.Error("the record must still be Active(): whether this machine can run it is a " +
			"different question from whether anything vouched for it, and collapsing the two " +
			"would misreport the reason")
	}
	for _, h := range set.Honored() {
		if h.Name == "acme-proxy" {
			t.Error("Honored() includes a record nothing vouched for — it is the predicate " +
				"for what actually crossed")
		}
	}
}
