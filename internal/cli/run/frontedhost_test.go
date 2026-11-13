package run

// frontedhost_test.go pins the launch's half of the FRONTED HOST LOOPHOLE (loopholes.Set's
// FrontedHostNames; docs/reference/git-broker.md ID-6): a pack env pointer `served_by`
// git-broker, packs/git's GIT_CONFIG_* rewrite, is served exactly where the launch starts and
// fronts that host daemon, and named with its own reason everywhere else. Through the real
// jailDaemonsFor and servedDaemons, so deleting either call site fails it.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestTheLaunchServesTheGitRewriteOnlyWhereItFrontsGitBroker(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	packs := []*packload.Pack{officialPack(t, "git")}
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	loopholes.SetPackModules(packLoopholeModules(packs))
	for _, tc := range []struct {
		runtime string
		enabled bool
		serves  bool
		why     []string
	}{
		{"podman", true, true, nil},
		{"macos-user", true, true, nil},
		// A broker has no switch (docs/design/boundary-broker.md BB-D106): a config switch of
		// git-broker off changes nothing, so the rewrite is served wherever the broker runs.
		{"podman", false, true, nil},
		{"macos-user", false, true, nil},
		{"container", true, false, []string{"Apple Container", "runs no host services"}},
	} {
		cfg := bareConfig()
		block, entry := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
		entry.Set("enabled", tc.enabled)
		block.Set("git-broker", entry)
		cfg.Set("loopholes", block)
		o := goldenOptions("/ws", t.TempDir())
		o.runtime = tc.runtime
		served := o.servedDaemons(o.jailDaemonsFor(cfg, tc.runtime, packs))
		if got := served.Serves("git-broker"); got != tc.serves {
			t.Errorf("%s, enabled=%v: Serves(git-broker) = %v, want %v", tc.runtime, tc.enabled, got, tc.serves)
		}
		scope, err := packload.ScopeCredentials(packload.ScopeInput{Packs: packs, NoDerives: true, Served: &served})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Join(packload.UnservedLines(scope, nil, nil), "\n")
		for _, want := range tc.why {
			if !strings.Contains(lines, want) {
				t.Errorf("%s, enabled=%v: the withheld rewrite's line does not say %q:\n%s",
					tc.runtime, tc.enabled, want, lines)
			}
		}
		if tc.serves && strings.Contains(lines, "GIT_CONFIG_COUNT") {
			t.Errorf("%s: a launch that fronts git-broker withheld the rewrite:\n%s", tc.runtime, lines)
		}
	}
}
