package render

// configkeys_test.go is the config-key census's drift gate (docs/design/declaration-parity.md
// OQ-DP5, DP-B31), in internal/config/inherit_test.go's three directions: every live key is
// classified, nothing classified is a key the schema lacks, and every classification says why.
// The report half, that `yolo host apply` names what this census says it leaves undone, is
// pinned through the command itself in internal/cli (hostconfigkeys_test.go).

import (
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestHostConfigKeyCensusIsTotal is the forcing function: a key added to the schema fails the
// build until the host census classifies it, so the next key cannot be silently absent at the
// host the way `packages` was until a human named it by hand.
func TestHostConfigKeyCensusIsTotal(t *testing.T) {
	var unclassified []string
	for _, key := range config.TopLevelConfigKeys() {
		if d, _ := HostFields().ConfigKey(key); d == KeyUnclassified {
			unclassified = append(unclassified, key)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("config keys with no host-notch classification: %v\n\n"+
			"Every live top-level key must be classified in hostConfigKeys "+
			"(internal/render/configkeys.go) as\n"+
			"  • KeyHonored       — some host-notch verb acts on it (name the reader), or\n"+
			"  • KeyNotApplicable — it means nothing off-container (say why), or\n"+
			"  • KeyUnbuilt       — it applies at the host and nothing honors it yet (say what is missing).\n"+
			"`yolo host apply` names every key of the last two your user config declares.",
			unclassified)
	}
}

// TestHostConfigKeyCensusHasNoPhantomKeys is the other direction: a key removed from the schema
// (or retired, which validation refuses before any target) must not leave an entry nothing reads.
func TestHostConfigKeyCensusHasNoPhantomKeys(t *testing.T) {
	live := map[string]bool{}
	for _, key := range config.TopLevelConfigKeys() {
		live[key] = true
	}
	var phantom []string
	for key := range hostConfigKeys {
		if !live[key] {
			phantom = append(phantom, key)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("hostConfigKeys classifies keys the live config schema does not have: %v — drop "+
			"the entry, or add the key to the schema", phantom)
	}
}

// TestEveryHostConfigKeyClassificationHasAReason: a disposition with no reason cannot be
// re-decided, which is inherit.go's argument and holds for honored keys too.
func TestEveryHostConfigKeyClassificationHasAReason(t *testing.T) {
	for key, e := range hostConfigKeys {
		if e.reason == "" {
			t.Errorf("hostConfigKeys[%q] has no reason", key)
		}
		if e.at == KeyUnclassified {
			t.Errorf("hostConfigKeys[%q] is listed with no disposition", key)
		}
	}
}

// A jail states no key census: its FieldSet answers KeyUnclassified for every key, rather than
// claiming the host-only keys (host_path, host_floor, ...) are honored in a container.
func TestJailFieldsStateNoKeyCensus(t *testing.T) {
	if d, why := JailFields().ConfigKey("packages"); d != KeyUnclassified || why != "" {
		t.Errorf("JailFields().ConfigKey(packages) = %v, %q; a jail states no key census", d, why)
	}
}

func TestLeftUndoneIsTheTwoDeclines(t *testing.T) {
	for d, want := range map[KeyDisposition]bool{
		KeyUnclassified: false, KeyHonored: false, KeyNotApplicable: true, KeyUnbuilt: true,
	} {
		if got := d.LeftUndone(); got != want {
			t.Errorf("%v.LeftUndone() = %v, want %v", d, got, want)
		}
	}
}

// `packages` is NOT applicable at the host, never unbuilt there: host-tool-provisioning.md's
// HP-DIR3 rules that at the host yolo never provisions the workspace's runtime, so
// provisioner-sets.md's OQ-PS1 gives the darwin materializer no host caller. KeyUnbuilt would
// say a reader is coming, which is the build that ruling declines.
func TestPackagesIsNotApplicableAtTheHost(t *testing.T) {
	if d, _ := HostFields().ConfigKey("packages"); d != KeyNotApplicable {
		t.Errorf("HostFields().ConfigKey(packages) = %v, want KeyNotApplicable (HP-DIR3)", d)
	}
}

// `brokered` widens a jail's GitHub broker for one workspace, and only a jail launch starts that
// broker (writeScopeFiles in internal/cli/run), so at the host it is not applicable rather than
// unbuilt: no host verb is waiting to read it.
func TestBrokeredIsNotApplicableAtTheHost(t *testing.T) {
	if d, _ := HostFields().ConfigKey("brokered"); d != KeyNotApplicable {
		t.Errorf("HostFields().ConfigKey(brokered) = %v, want KeyNotApplicable", d)
	}
}
