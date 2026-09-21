package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// NO SHIPPED MANIFEST DECLARES A RETIRED HOOK, and this is the half of the retirement
// that a deletion in packdecl alone would leave broken.
//
// `claude_plugins` was declared by packs/claude's own pack.json. Removing the name from
// the closed set without removing the declaration turns yolo's flagship pack into a
// manifest yolo REFUSES: `yolo check` red on a stock config, and `yolo pack` unable to
// stage the tree. TestEveryEmbeddedPackHookIsHonored next door catches a hook that is
// declared but unimplemented; this catches one that is declared and RETIRED.
//
// ⚠ IT READS THE MANIFEST BYTES AND DECODES THEM STRICTLY, and the first version of this
// test did neither — it walked p.Decl.HookContributions(), which comes from the TOLERANT
// decoder, which SKIPS a retired hook by design. So putting the declaration back left it
// green: the contribution was dropped before the assertion could see it. Strict decode is
// also the truer subject, since Decode is what the host path runs over a staged tree.
//
// Every embedded pack, not claude alone: a retirement is a fact about the vocabulary, so
// any pack that picked the name up is equally wrong.
func TestNoEmbeddedPackDeclaresARetiredHook(t *testing.T) {
	packs, err := embeddedPackSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) == 0 {
		t.Fatal("no embedded packs — this test has lost its subject")
	}
	for _, p := range packs {
		data, err := os.ReadFile(filepath.Join(p.Root, packdecl.ManifestName))
		if err != nil {
			t.Fatalf("pack %s: %v", p.Name, err)
		}
		if _, problems := packdecl.Decode(data); len(problems) != 0 {
			t.Errorf("pack %s no longer validates strictly, which is what `yolo check` "+
				"and pack staging run:\n  %s", p.Name, strings.Join(problems, "\n  "))
		}
	}
}

// THE DISPATCH NO LONGER HONORS THE NAME. The manifest half above and the closed-set half
// in packdecl both concern what an author may WRITE; this is the one assertion about what
// the boot RUNS, and without it the retirement is a documentation change.
//
// It reaches runPackHook directly with a hook a manifest can no longer carry, because that
// is the only way to ask the switch the question: a pack cannot deliver the name any more.
// An error — not a silent no-op — is the right answer for the same reason the default arm
// gives: the known set and the switch disagreeing is a yolo bug, and the boot surfaces it.
func TestTheRetiredHookIsNotDispatched(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	err = runPackHook(e, p, packdecl.Hook{Name: "claude_plugins"})
	if err == nil {
		t.Fatal("runPackHook still honors claude_plugins — the retirement (OQ-2) removed " +
			"the arm, so reaching one means the hook still runs the vendor CLI at boot")
	}
	if !strings.Contains(err.Error(), "claude_plugins") {
		t.Errorf("the refusal does not name the hook it refused: %v", err)
	}
}

// The two surviving hooks still RUN. Every assertion above is satisfied by deleting the
// hook mechanism outright, which would take shared credentials and per-jail history with
// it — the "I have to log in again in every jail" regression, arrived at from the other
// direction. Their behaviour is pinned in hookdrift_test.go; what is pinned HERE is that
// the dispatch still has an arm for each, so the retirement did not empty the switch.
func TestTheSurvivingHooksAreStillDispatched(t *testing.T) {
	for _, name := range []string{HookSharedCredentials, HookPerJailHistory} {
		e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
		p, err := embeddedPack("claude")
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately parameterless, so each arm answers with ITS OWN validation
		// complaint rather than running. The default arm's "unimplemented hook" is the
		// failure being excluded: it is what a deleted arm looks like.
		err = runPackHook(e, p, packdecl.Hook{Name: name})
		if err != nil && strings.Contains(err.Error(), "unimplemented hook") {
			t.Errorf("hook %q fell through to the default arm — its dispatch was deleted "+
				"with the retired one", name)
		}
	}
}
