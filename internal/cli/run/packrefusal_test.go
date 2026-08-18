package run

// packrefusal_test.go pins OQ-TP6 (docs/design/trust-paths.md §3.1): a refused contribution
// refuses the LAUNCH.
//
// # What was broken, and what a test of it has to look like
//
// The host already computed every refusal in this file correctly. It printed them, and then
// staged the unmodified pack.json anyway — and the jail re-derived the verdict from a hardcoded
// `mayAccessHost = true`, so the refusal branch was unreachable there and the curl-to-bash
// launcher got written for a fetched, unapproved pack. A test over the PREDICATE could never
// have caught that: the predicate answered right, every time, and was then ignored.
//
// So the assertions here are about the LAUNCH, not about the predicate. The one that matters
// most is the simplest: stagePacks returns an error and hands back no packs. Everything
// downstream of it — the shims, the surfaces, the mounts, the jail — is unreachable from a
// launch that did not happen, which is the whole point of closing this by ruling instead of by
// plumbing a decision across the host/jail boundary.
//
// # And the message, which is half the work
//
// A user with a selected-but-unapproved fetched pack used to get a warning and a working jail.
// They now get no jail, so this message is the entire user experience of the failure. A fatal
// the reader cannot act on would be strictly worse than the warning it replaced, which is why
// the naming assertions below are as load-bearing as the refusal ones.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fetchedPackWithClaims builds a git+file:// pack — genuinely OriginFetched, with no network —
// whose pack.json carries the given contribution bodies, installs it into the store the launch
// resolves from, and selects it in the user config.
//
// git+file:// rather than a hand-written lockfile, for the same reason fetchedLoopholePack uses
// it: packMayAccessHost keys on PackEntry.Origin(), so only a real fetched address exercises the
// unapproved branch. The store is populated the way `yolo pack install` would MINUS the
// approval, which is exactly the situation under test — installed, selected, never approved.
func fetchedPackWithClaims(t *testing.T, home, contributes string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	pj := `{"name":"acme","contributes":[` + contributes + `]}`
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
	syncPackStore(t, src)
}

// THE RULING, end to end and through the production path: a fetched pack whose curl-piped
// installer was never approved yields NO JAIL.
//
// The npm install beside it is the control. The gate is per CONTRIBUTION on purpose — an npm
// install "is the same trust as any dependency the user already installs" — so a refusal that
// named it too would mean the fatal had collapsed to per-pack, which the comment on
// HonoredInstalls says is worse in both directions.
func TestUnapprovedInstallerRefusesTheLaunch(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeBundled(t)
	fetchedPackWithClaims(t, home,
		`{"kind":"program","bin":"acme","via":"installer","url":"https://acme.test/i.sh"},`+
			`{"kind":"program","bin":"tsx","via":"npm","package":"tsx"}`)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	root, loaded, briefings, err := o.stagePacks("yolo-test-installer-refusal")
	if err == nil {
		t.Fatalf("a fetched pack's UNAPPROVED curl-piped installer was refused and the launch "+
			"went ahead anyway. That is the §3.1 defect exactly: the host prints the refusal, "+
			"stages the pack unmodified, and the jail — whose mayAccessHost is hardcoded true — "+
			"writes the curl-to-bash launcher for it:\n%s", out.String())
	}
	// Nothing may come back beside the error. A caller that ignored it would otherwise build a
	// jail out of the packs yolo just declined, which is the same host-decides/jail-executes
	// split one layer up.
	if root != "" || len(loaded) != 0 || len(briefings) != 0 {
		t.Errorf("a refused launch still returned root=%q packs=%d briefings=%d",
			root, len(loaded), len(briefings))
	}

	got := err.Error()
	// WHICH PACK and WHICH CLAIM. "a claim was refused" would send the reader to read every
	// manifest they have.
	for _, want := range []string{"acme", "https://acme.test/i.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	// AND THE THREE CHOICES, each with something the reader can actually do. This is the half
	// the ruling's own warning callout says must not be skipped: "a fatal the reader cannot act
	// on would be worse than the warning it replaces."
	for _, want := range []string{
		"FIX", "REMOVE", "APPROVE",
		"yolo pack install",    // the command for the approve path
		paths.UserConfigPath(), // where the remove path happens
		"trust-paths.md",       // why this is a refusal and not a warning
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal is missing %q — this message is the ENTIRE user experience "+
				"of the failure, because there is no jail left to go looking in:\n%s", want, got)
		}
	}
	// The CONTROL: an npm install is deliberately not origin-gated, so it must not be dragged
	// into the refusal by a gate that decided once for the whole pack.
	if strings.Contains(got, "tsx") {
		t.Errorf("the refusal names the pack's npm install, which is deliberately ungated — "+
			"the gate is per CONTRIBUTION, or a fetched pack can smuggle an installer URL "+
			"through beside one:\n%s", got)
	}
}

// EVERY gated claim, not just the installer. §3.1's scope is "refusals in general", and the
// claim kinds are what `pack install` prompts for one by one — so each of them has to reach the
// fatal, naming its own specific target.
//
// The approved half of each case is not a formality: it is what makes the gate a gate rather
// than a ban, and without it every assertion above passes for a build that refuses all packs.
func TestEveryGatedClaimRefusesTheLaunchNamingItsTarget(t *testing.T) {
	for _, tc := range []struct {
		kind        string
		contributes string
		target      string
		// ungated marks a claim that is NOT origin-gated anywhere, so a fetched pack still
		// gets it and no refusal may mention it.
		ungated bool
	}{
		{kind: "reads-host", contributes: `{"kind":"reads-host","host":".netrc"}`, target: ".netrc"},
		{kind: "mount", contributes: `{"kind":"mount","host":"datasets/acme","into":"acme"}`,
			target: "datasets/acme"},
		{kind: "installer", contributes: `{"kind":"program","bin":"acme","via":"installer",` +
			`"url":"https://acme.test/i.sh"}`, target: "https://acme.test/i.sh"},
		// The claim that had no reporter at all until this ruling: the launch withheld it in
		// one `&& p.MayAccessHost` inside prepare.go and said nothing, so a pack whose only
		// host claim was "prepend the user's own AGENTS.md" produced a jail with its prose and
		// none of the user's.
		{kind: "briefing overlay", contributes: `{"kind":"briefing","into":"AGENTS.md",` +
			`"after":"host:AGENTS.md"}`, target: "AGENTS.md"},
		{kind: "env", contributes: `{"kind":"env","vars":{"ACME_OK":"1"}}`,
			target: "ACME_OK", ungated: true},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pack.json"),
				[]byte(`{"name":"acme","contributes":[`+tc.contributes+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			fetched, probs := packload.LoadDir(root, "acme", false)
			if len(probs) > 0 {
				t.Fatalf("fixture: %v", probs)
			}
			joined := strings.Join(packRefusals(fetched), "\n")
			if tc.ungated {
				if joined != "" {
					t.Errorf("%s is not origin-gated, so an unapproved pack still gets it and "+
						"refusing the launch over it would break packs that work today:\n%s",
						tc.kind, joined)
				}
				return
			}
			if joined == "" {
				t.Fatalf("an unapproved fetched pack's %s claim produced NO refusal, so the "+
					"launch proceeds and the claim is withheld in silence — the partial pack "+
					"OQ-TP6 retires", tc.kind)
			}
			if !strings.Contains(joined, tc.target) {
				t.Errorf("the %s refusal does not name %q, so the reader cannot tell which of "+
					"the pack's claims to fix, remove or approve:\n%s", tc.kind, tc.target, joined)
			}
			if !strings.Contains(joined, "acme") {
				t.Errorf("the %s refusal does not name the pack:\n%s", tc.kind, joined)
			}

			// The gate is a GATE: an origin that permits host access refuses nothing.
			approved, _ := packload.LoadDir(root, "acme", true)
			if r := packRefusals(approved); len(r) != 0 {
				t.Errorf("an embedded/local/approved pack's %s claim was refused, which would "+
					"make every one of yolo's own packs a refused launch: %v", tc.kind, r)
			}
		})
	}
}

// A WRAPPED PLUGIN'S HOOK BODIES, which is new coverage rather than a changed outcome: the
// launch path never called HonoredPlugins at all.
//
// That was the hole. A plugin travels INSIDE the pack's skills tree, which the launch stages
// into the jail whole, so a fetched pack's unapproved `hooks` reached the agent's lifecycle
// events with the refusal computed nowhere on this path. It is row 21 of the trust inventory —
// "the weakest claim string in the system" — and the ruling is what closes it.
func TestUnapprovedPluginHooksRefuseTheLaunch(t *testing.T) {
	root := t.TempDir()
	md := filepath.Join(root, "skills", "acme-tools", ".claude-plugin")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(`{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"acme","contributes":[{"kind":"skills","from":"skills",`+
			`"into":".claude/skills"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fetched, probs := packload.LoadDir(root, "acme", false)
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	joined := strings.Join(packRefusals(fetched), "\n")
	if joined == "" {
		t.Fatal("a fetched pack's unapproved plugin HOOKS produced no refusal, so the launch " +
			"proceeds and the hook bodies are staged into the jail for the agent to run at its " +
			"own lifecycle events")
	}
	for _, want := range []string{"acme-tools", "hooks"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the plugin refusal does not name %q:\n%s", want, joined)
		}
	}

	// A SKILLS-ONLY plugin is content, and content is the thing a pack distributes. Refusing a
	// launch over one would make the ruling a ban on wrapping plugins at all.
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(`{"name":"acme-tools","skills":["./"],"agents":["./agents"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	contentOnly, _ := packload.LoadDir(root, "acme", false)
	if r := packRefusals(contentOnly); len(r) != 0 {
		t.Errorf("a plugin that runs no code was refused: %v", r)
	}
}

// THE TWO GUARDS, which the ruling names explicitly and which a wider reading of "no partial
// packs" would have swept up. Both are deliberate, and a jail's ability to boot depends on the
// second.
//
// The line that separates them from the ruling: it is about a claim yolo UNDERSTOOD and
// DECLINED. Something absent, or something from the future, is neither.
func TestAbsenceAndSkewAreNotRefusals(t *testing.T) {
	// GUARD 1 — a declared bind mount whose HOST PATH IS ABSENT (loopholes/runtime.go skips it
	// with a warning). Nothing was refused: the origin permits the loophole, the user consented
	// to the capability, and the thing simply is not there. Driven through a real loophole pack
	// so the fixture is the same shape as the refused one, differing only in the fact that
	// makes it adaptation rather than refusal.
	//
	// TWO HALVES, and the second is what makes the first mean anything. `packRefusals` staying
	// silent is necessary and NOT SUFFICIENT: it never stats a host path, and every producer in
	// it short-circuits on MayAccessHost — which this fixture has, being a local pack — long
	// before anything could look at one. Measured 2026-08-18: the silent half alone passes with
	// the source pointed at a path that EXISTS, and passes with the manifest carrying no
	// `host_bind_mounts` key at all, so on its own it says nothing about absence.
	//
	// The argv is where the absence is actually decided, so that is where the second half
	// reads, and it asserts in BOTH directions: the missing source is dropped, and a present
	// source beside it still crosses. Without the second direction "skipped" degenerates into
	// "bind mounts do not work", which passes the first direction perfectly.
	// The fixture is built here rather than through writeRealLoopholePack because it needs the
	// module dir back: a pack-shipped loophole may only bind a path under `{loophole_dir}` or
	// one relative to the home (loophole-packaging.md §3.1), so "present" and "absent" have to
	// be two names inside the module the pack ships — one written, one not. An absolute /tmp
	// path fails that check and takes the whole manifest down with it, which is a fourth way
	// for this guard to pass while measuring nothing.
	//
	// YOLO_VERSION is cleared for the same reason every other launch-path test in this package
	// clears it: the argv is a HOST-side computation, and in-jail a loophole with bind mounts
	// is Active() only if one of its CONTAINER paths already exists (loopholes.inJailActive).
	// Left set, the whole loophole drops out before runtimeArgsFor looks at a single source and
	// the argv is empty for a reason that has nothing to do with absence — a fifth way for this
	// guard to pass while measuring nothing, and the one that would have hidden the other four.
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeBundled(t)
	packRoot := t.TempDir()
	mod := filepath.Join(packRoot, "loopholes", "acme-proxy")
	if err := os.MkdirAll(filepath.Join(mod, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(
		`{"name":"acme-proxy","transport":"none","default_enabled":true,`+
			`"host_daemon":{"cmd":["/bin/true"],"publishes":"socket"},`+
			`"host_bind_mounts":[{"host":"{loophole_dir}/conf","container":"/etc/acme-present"},`+
			`{"host":"{loophole_dir}/gone","container":"/etc/acme-absent"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), []byte(
		`{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	absentSrc := filepath.Join(mod, "gone")
	absent, probs := packload.LoadDir(packRoot, "acme", true)
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	if r := packRefusals(absent); len(r) != 0 {
		t.Errorf("an absent bind SOURCE refused the launch: %v\nThat is adaptation inside a "+
			"capability the user already consented to — a host path that is not there is not a "+
			"claim yolo declined, and making it fatal would refuse launches on any machine "+
			"where an optional host dir does not exist", r)
	}
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{absent}))
	argv := strings.Join((&Options{}).loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman"), " ")
	if strings.Contains(argv, absentSrc) {
		t.Errorf("the absent bind source reached the container argv:\n%s\nThe launch does not "+
			"refuse over it, so something has to drop it — and podman's own error for a "+
			"nonexistent -v source is a boot failure naming neither the pack nor the loophole, "+
			"which is the refusal this guard says is not happening, wearing a worse message",
			argv)
	}
	if !strings.Contains(argv, "/etc/acme-present") {
		t.Errorf("the PRESENT bind mount beside it did not cross either:\n%s\nSkipping what is "+
			"missing is adaptation; skipping what is there is a loophole that does not work, "+
			"and the assertion above cannot tell the two apart on its own", argv)
	}

	// GUARD 2 — a contribution whose KIND this build does not recognise. The jail's manifest
	// reads are cross-version by construction (the host CLI is freshly built, the entrypoint is
	// baked into the image at the last `just load`), so an unknown kind is SKIPPED and reported
	// as a SkewNote. Fatal here would mean a jail that will not start over a pack yolo itself
	// shipped, with no way for the user to route around it.
	//
	// Asserted against a Pack carrying SkewNotes directly rather than by flipping packload's
	// process-wide TolerateSkew, which has no reset and would leak into every later test in this
	// binary. What is being pinned is that packRefusals never reads that field, and this says it
	// exactly.
	future := &packload.Pack{
		Name: "acme", MayAccessHost: false, Decl: &packdecl.Manifest{},
		SkewNotes: []string{`pack acme: contribution 1: unknown kind "telepathy" — skipped`},
	}
	if r := packRefusals(future); len(r) != 0 {
		t.Errorf("a contribution FROM THE FUTURE refused the launch: %v\nSkew tolerance is not "+
			"a refusal — the cost of getting this wrong is the boot path, where a jail does not "+
			"start at all", r)
	}
}

// The refusal ACCUMULATES across the whole configured set and states the three choices ONCE.
//
// Raised after the staging loop rather than inside it for a reason a user feels: a person with
// two broken packs should learn about both in one launch, not fix one, relaunch, and discover
// the second. And the three choices are advice about the situation, not about each claim —
// repeating them per refusal would bury the claims they are supposed to be about.
func TestRefusalNamesEveryClaimAndAdvisesOnce(t *testing.T) {
	err := refusedLaunchError([]string{
		`pack acme: refused installer "https://acme.test/i.sh" — …`,
		`pack other: refused mount "datasets/x" — …`,
	})
	got := err.Error()
	for _, want := range []string{"acme", "other", "https://acme.test/i.sh", "datasets/x"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal dropped %q — a second broken pack must not need a second "+
				"launch to be discovered:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "APPROVE"); n != 1 {
		t.Errorf("the three choices appear %d times, want exactly 1 — repeated advice buries "+
			"the claims it is advice about:\n%s", n, got)
	}
}
