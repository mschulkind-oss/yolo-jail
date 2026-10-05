package cli

// hosttreesnotch_test.go pins PPX-D34 at the host notch (docs/design/patched-extensions.md): a tree
// whose list entry reaches the host (a guarded posture list) is built and linked by `yolo host apply
// --assert`, while one whose entry reaches jails alone (an autonomous posture list) is neither built
// nor linked there, and a link to it an earlier render left is retired with its copies.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
)

const treeConfigList = `{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/.tool/ext/tool-ext"]}`

// treePosture is an autonomy contribution whose posture's list on `tool/settings` names the tree.
func treePosture(posture string) string {
	return `{"kind":"autonomy","` + posture + `":{"lists":[{"surface":"tool/settings","path":"/packages",` +
		`"add":["~/.tool/ext/tool-ext"]}]}}`
}

// A GUARDED-ONLY TREE is still the host's: `yolo host apply --assert` builds it and links `~/<into>`.
func TestHostApplyInstallsAGuardedOnlyTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgentWith(t, treePosture("guarded"))
	fx.writeHostConfig(t, "")
	stubBins(t, "tool") // the dep probe's: the agent pack's program is on the host
	if f := fx.tree(t); f.Owner != "agentpack" || f.ListedInJail || !f.ListedAtHost {
		t.Fatalf("the fixture's tree: owner %q, jail %v, host %v", f.Owner, f.ListedInJail, f.ListedAtHost)
	}
	var errw bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	if len(fx.builds) != 1 {
		t.Fatalf("--assert built %d trees, want the guarded one:\n%s", len(fx.builds), errw.String())
	}
	if target, err := os.Readlink(fx.link()); err != nil || !isDir(target) {
		t.Errorf("--assert did not link the guarded tree (%q, %v)", target, err)
	}
}

// A TREE NO HOST LOADS is neither built nor linked by `yolo host apply --assert`. Red if
// advanceHostTrees or the render stops reading where the list entry reaches.
func TestHostApplyBuildsAndLinksNoTreeListedForJailsAlone(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgentWith(t, treePosture("autonomous"))
	fx.writeHostConfig(t, "")
	stubBins(t, "tool") // the dep probe's: the agent pack's program is on the host
	var out, errw bytes.Buffer
	hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader(""))
	if len(fx.builds) != 0 {
		t.Errorf("--assert built %d trees no host loads:\n%s", len(fx.builds), errw.String())
	}
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Errorf("--assert linked a tree no host loads:\n%s", out.String())
	}
}

// A LINK AN EARLIER RENDER LEFT goes once the tree's entry reaches jails alone: the dry run names it
// and keeps it, and --assert removes it, its versioned copies and its ownership record entry. Red if
// the render stops retiring it.
func TestHostApplyRetiresTheLinkOfATreeThatNoLongerReachesTheHost(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgentWith(t, treeConfigList)
	fx.writeHostConfig(t, "")
	stubBins(t, "tool") // the dep probe's: the agent pack's program is on the host
	var out, errw bytes.Buffer
	hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader(""))
	target, err := os.Readlink(fx.link())
	if err != nil || !isDir(target) {
		t.Fatalf("the first apply rendered no link (%q, %v):\n%s%s", target, err, out.String(), errw.String())
	}
	manifest := filepath.Join(fx.treeDir, "pack.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(data), treeConfigList, treePosture("autonomous"), 1)
	if moved == string(data) {
		t.Fatal("the fixture's list entry was not moved")
	}
	writeFile(t, manifest, moved)
	out.Reset()
	hostApply(nil, &out, &errw, false, nil)
	if !strings.Contains(out.String(), "would remove") || !isDir(target) {
		t.Fatalf("the dry run does not name the link it would remove, or removed it:\n%s", out.String())
	}
	out.Reset()
	hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader(""))
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Errorf("the link of a tree no host loads was kept:\n%s", out.String())
	}
	if isDir(filepath.Dir(target)) {
		t.Errorf("its versioned copies were kept:\n%s", out.String())
	}
	if man, _ := hostskills.LoadManifest(hostSkillsManifestPath()); man.OwnedBy(fx.link(), "treepack") {
		t.Error("the ownership record still names the retired link")
	}
	if len(fx.builds) != 1 {
		t.Errorf("the applies built %d trees, want the first one's alone", len(fx.builds))
	}
}
