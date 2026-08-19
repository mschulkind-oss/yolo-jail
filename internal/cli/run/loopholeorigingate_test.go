package run

// loopholeorigingate_test.go pins §4.3 G3 AT THE SPAWN AND THE ARGV, not at the predicate:
// "a fetched pack whose loophole claim is unapproved has its loophole NOT DISCOVERED AT ALL
// while its other contributions still work."
//
// # Why this is end-to-end and not a unit test of the gate
//
// The gate was already CORRECT and already IGNORED. HostExecApproved was computed per module
// by packLoopholeModules (pinned by TestPackLoopholeModulesCarryTheOriginGate) and had exactly
// one production reader — runDoctorChecks. ManifestHostDaemonSpecs filtered on
// FromConfig/HostDaemon/Active and never consulted it; RuntimeArgsFor did not either. So an
// unapproved fetched pack's daemon entered the spawn list and RAN, and its binds, devices,
// intercepts and CA reached the container argv, while packMayAccessHost correctly returned
// false and the comment above packLoopholeModules said the decision was "the SAME gate, not a
// second one that could disagree" — true of the DECISION, false of its ENFORCEMENT.
//
// A test over the predicate cannot see that: the predicate answers right. The assertion has to
// be that the daemon did not run (a sentinel file it would have written) and that the argv does
// not carry the loophole's crossings, driven through the production path from a real pack.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fetchedLoopholePack builds a GIT pack (git+file://, so it is genuinely OriginFetched with
// no network) whose one loophole declares a host_daemon that TOUCHES A SENTINEL, plus a bind
// mount, a device and an intercept. It installs the pack into the store the launch reads,
// writes the user config that selects it, and returns the sentinel path.
//
// git+file:// rather than a hand-written lockfile: `packMayAccessHost` keys on
// PackEntry.Origin(), so only a real fetched address exercises the unapproved branch. And the
// pack must actually be in the store, because launch resolution is strictly offline.
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
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
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

// TestUnapprovedFetchedPackLoopholeNeitherSpawnsNorReachesTheArgv is G3, measured — as
// OQ-TP6 rewrote it.
//
// UPDATED 2026-08-18. This test used to open on "staging must SUCCEED — G3 refuses the
// loophole, not the pack" and close on "AND THE PACK STILL WORKS", and both were faithful
// readings of G3. Both are retired by the ruling in docs/design/trust-paths.md §3.1: there are
// no partial packs, so a refused loophole refuses the LAUNCH. Assertion 4 is gone with the
// concept it asserted.
//
// The other three survive, and their REASON for surviving changed. They used to be the only
// thing standing between an unapproved fetched pack and a running host daemon; the refusal
// above them is now that thing. They are kept as DEFENCE IN DEPTH — driven from the tree
// staging actually wrote, with the module record made by hand because the refusal returns
// before stagePacks reaches the line that makes it — so a future relaxation of the fatal
// cannot silently re-open a hole it took four layers to close.
func TestUnapprovedFetchedPackLoopholeNeitherSpawnsNorReachesTheArgv(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeLoopholes(t)
	sentinel := fetchedLoopholePack(t, home)

	var out bytes.Buffer
	cname := "yolo-test-unapproved-loophole"
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out

	// 0. THE LAUNCH, which is now the outermost assertion and the one that makes the three
	//    below unreachable in production. A refused contribution refuses the launch.
	_, loaded, _, err := o.stagePacks(cname)
	if err == nil {
		t.Fatalf("staging SUCCEEDED for a pack whose loophole was refused — OQ-TP6 rules that "+
			"a partial pack is not a thing yolo runs:\n%s", out.String())
	}
	if len(loaded) != 0 {
		t.Errorf("a refused launch still handed %d packs to the rest of the pipeline; the "+
			"refusal must be the whole return value, or a caller ignoring the error gets a "+
			"jail built from packs yolo just declined", len(loaded))
	}
	// AND THE MODULE WAS NEVER RECORDED. stagePacks calls loopholes.SetPackModules on its way
	// out; the refusal returns first, so the converged set never learns the loophole exists.
	// That is the end-to-end shape of "nothing of it crossed" under the ruling — no gate had
	// to hold, because no gate was ever consulted.
	if _, ok := loopholes.NewHostSet(nil).Lookup("acme-proxy"); ok {
		t.Error("a refused launch still recorded the pack's loophole into the converged set")
	}

	// The three original assertions, now as defence in depth. Load the pack from the STAGED
	// tree (staging ran; only the return did not) and make the module record stagePacks would
	// have made, so the layers below the fatal are exercised rather than merely bypassed.
	staged := filepath.Join(paths.AgentsDir(), cname, "packs", config.PackEntry{Name: "acme"}.Slug())
	p, probs := packload.LoadDir(staged, "acme", false)
	if len(probs) > 0 {
		t.Fatalf("the staged tree does not load: %v", probs)
	}
	// Precondition: the pack itself is unapproved. Without this the test could pass because
	// the pack failed to load at all.
	if p.MayAccessHost {
		t.Fatal("the fixture pack was granted host access — it must be a FETCHED pack with " +
			"no lockfile approval, or the gate under test is not the branch being exercised")
	}
	loaded = []*packload.Pack{p}
	loopholes.SetPackModules(packLoopholeModules(loaded))

	// 1. THE SPAWN. startLoopholes is reached through its one disclosed call site.
	o.startLoopholesDisclosed(cname, "podman", jsonx.NewOrderedMap(), loaded)
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("an UNAPPROVED fetched pack's host daemon EXECUTED. G3 says its loophole is " +
			"\"not discovered at all\"; the gate was computed per module and then never " +
			"consulted by ManifestHostDaemonSpecs")
	}

	// 2. THE ARGV. Every other crossing the manifest declares must be absent too: an
	// unapproved loophole that does not spawn but still bind-mounts a host dir, passes a
	// device and installs a CA is the same defect one layer down.
	argv := strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman"), " ")
	for _, forbidden := range []string{
		"api.acme.test",       // --add-host
		"/etc/acme",           // bind mount destination
		"/dev/null",           // device passthrough
		"NODE_EXTRA_CA_CERTS", // the CA
	} {
		if strings.Contains(argv, forbidden) {
			t.Errorf("the container argv carries %q from an UNAPPROVED fetched pack's loophole "+
				"— the origin gate governs the whole loophole, not just the daemon:\n%s",
				forbidden, argv)
		}
	}

	// 3. THE BRIEFING. Same shape one surface over: the briefing is INSTRUCTIONS THE AGENT
	// ACTS ON, so advertising a loophole whose daemon was withheld and whose binds never
	// happened sends the agent to debug host wiring that was deliberately refused. An
	// unapproved loophole is Active() — enabled, right platform, requires met — which is
	// exactly why Active() is the wrong predicate here.
	for _, lo := range briefingLoopholes(nil) {
		if lo.Name == "acme-proxy" {
			t.Error("the jail briefing advertises an UNAPPROVED pack's loophole as a live " +
				"capability. Nothing of it crossed, so the agent is being told to use a " +
				"capability that is not there")
		}
	}

	// 4. WAS "AND THE PACK STILL WORKS" — G3's "its other contributions still work". Deleted
	// 2026-08-18 rather than inverted: OQ-TP6 retires the partial pack, so there is no jail
	// for the pack's `env` contribution to work in. Assertion 0 above is what replaced it, and
	// the fixture keeps its second contribution so the refusal is demonstrably about the
	// loophole and not about a pack with nothing else in it.
}

// An APPROVED origin still reaches both surfaces, or the gate is a ban rather than a gate and
// the assertions above would pass for a loophole mechanism that never works at all.
//
// A file:// pack: its origin carries the user's own authority, which is what makes the TRUE
// branch observable without a lockfile.
func TestApprovedPackLoopholeReachesTheSpawnAndTheArgv(t *testing.T) {
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
		t.Errorf("an APPROVED pack loophole's host daemon did not start — the gate must be a "+
			"gate, not a ban:\n%s", out.String())
	}
	argv := strings.Join(o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman"), " ")
	for _, want := range []string{"api.acme.test", "/etc/acme"} {
		if !strings.Contains(argv, want) {
			t.Errorf("an APPROVED pack loophole's %q is missing from the argv:\n%s", want, argv)
		}
	}
	// And the record IS in the converged set: G3's refusal is "not discovered at all", so its
	// converse has to be observable in the same place.
	if _, ok := loopholes.NewHostSet(nil).Lookup("acme-proxy"); !ok {
		t.Error("an approved pack loophole is missing from the converged discovery set")
	}
	// And the briefing DOES advertise it: Honored() must not be a synonym for "no pack
	// loophole is ever mentioned", which would make assertion 3 above pass vacuously.
	advertised := false
	for _, lo := range briefingLoopholes(nil) {
		if lo.Name == "acme-proxy" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("an APPROVED pack loophole is missing from the jail briefing — the gate must " +
			"be a gate, not a rule that hides every pack loophole from the agent")
	}
}

// AN UNAPPROVED PACK LOOPHOLE IS STILL DISCOVERED AND LISTED, and that is the reconciliation
// of G3's "not discovered at all" with §5.1's visibility requirement: nothing of it CROSSES,
// while `yolo loopholes list`/`status` still show it — as `unapproved`.
//
// It matters because the two failure modes are not symmetric. A withheld crossing is the
// intended outcome; a loophole MISSING from the listing is indistinguishable from a pack that
// failed to stage, and the fix ("`yolo pack install` records the approval") is not
// discoverable from an absence. Pinned here, beside the refusal, so a later "not discovered
// at all" taken literally fails this test rather than a user's debugging session.
func TestUnapprovedPackLoopholeIsStillListed(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeLoopholes(t)
	mod := writeLoopholeModule(t, t.TempDir(), "acme-proxy", "")
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: false}})

	set := loopholes.NewHostSet(nil)
	lp, ok := set.Lookup("acme-proxy")
	if !ok {
		t.Fatal("an unapproved pack loophole vanished from the converged set — a missing entry " +
			"reads as a pack that failed to stage, and the route to approving it is not " +
			"discoverable from an absence")
	}
	if set.MayRunHostCode(lp) {
		t.Fatal("the fixture is wrong: the record must be unapproved")
	}
	// Active but NOT honored — the distinction the briefing needed.
	if !lp.Active() {
		t.Error("the record must still be Active(): the gate is about its ORIGIN, not about " +
			"whether this machine can run it, and collapsing the two would misreport the reason")
	}
	for _, h := range set.Honored() {
		if h.Name == "acme-proxy" {
			t.Error("Honored() includes an unapproved pack loophole — it is the predicate for " +
				"what actually crossed")
		}
	}
}
