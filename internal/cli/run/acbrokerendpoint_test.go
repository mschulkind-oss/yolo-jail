package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// brokerAndOpenAIFixtureModules makes BOTH broker manifests this process's whole
// pack-module record — the claude broker and the OpenAI one — because the Apple
// Container shape under test needs both records at once and neither existing fixture
// registers two.
//
// It is the `packs: ["claude", "codex"]` jail in fixture form: codex brings the OpenAI
// loophole, which is the only thing that gets an AC launch past hostServicesMountArgs'
// early return, and claude brings the broker record that used to be emitted there.
func brokerAndOpenAIFixtureModules(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	mods := make([]loopholes.PackModule, 0, 2)
	for _, name := range []string{broker.BrokerLoopholeName, openAIAuthBrokerName} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// `publishes: "socket"` is REQUIRED of a pack-shipped daemon (the default,
		// "endpoint", is refused at load and the loophole silently vanishes).
		manifest := `{"name":"` + name + `","description":"fixture","version":1,` +
			`"default_enabled":true,"transport":"` + loopholes.TransportLoopbackTLS + `",` +
			`"lifecycle":"spawned",` +
			`"host_daemon":{"cmd":["yolo","internal","daemon","` + name + `","--socket","{socket}"],` +
			`"publishes":"socket","scope":"host"},` +
			`"jail_daemon":{"cmd":["yolo-jaild","oauth-terminator"],"restart":"on-failure"}}`
		if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		mods = append(mods, loopholes.PackModule{Dir: dir, HostExecApproved: true})
	}
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules(mods)
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

// TestAppleContainerWithholdsTheClaudeBrokerEndpoint: on Apple Container the argv must
// not name the claude broker's endpoint file, because nothing in an AC launch is asked
// to write it.
//
// Two independent halves of the pipeline decline, and neither is a race: run.go ensures
// the host-wide singleton only when `rt != "container"`, and startLoopholes' per-runtime
// allow list admits `openai-auth-broker` alone on this backend. The variable was emitted
// anyway whenever the broker loophole was active, so an AC jail with
// `packs: ["claude", "codex"]` sent its in-jail terminator at a path that never appears
// — and since the reachability witness became fatal, an unpublished endpoint can refuse
// the whole launch (OQ-R4, OQ-R5) for a service this backend never runs.
//
// The OpenAI endpoint is asserted PRESENT in the same argv, so the test cannot pass by
// suppressing host services wholesale: what is withheld is the promise nobody can keep,
// not the one this backend does keep.
func TestAppleContainerWithholdsTheClaudeBrokerEndpoint(t *testing.T) {
	brokerAndOpenAIFixtureModules(t)
	o := goldenOptions("/ws", t.TempDir())
	// A HOST launcher (not nested) with the singleton's socket present: every other
	// reason to suppress the variable is switched off, so only the backend is left.
	o.PathExists = func(string) bool { return true }
	o.Getenv = func(string) string { return "" }

	cfg := jsonx.NewOrderedMap()
	args := o.hostServicesMountArgs("container", "yolo-ws-abcd1234", cfg)

	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			t.Errorf("Apple Container argv promises the claude broker's endpoint file: %q\n"+
				"full args: %v\nNothing on an AC launch publishes it — the singleton ensure is "+
				"gated on `rt != \"container\"` and the loophole allow list admits "+
				"openai-auth-broker alone — so the in-jail terminator dials a file that never "+
				"appears, and the fatal witness may refuse the launch outright", a, args)
		}
	}
	// The one endpoint AC really does publish is still there.
	want := hostServiceEnvVar(openAIAuthBrokerName) + "=" + hostServiceEndpointPath(openAIAuthBrokerName)
	if !containsStr(args, want) {
		t.Fatalf("the OpenAI endpoint AC does publish went missing: %v", args)
	}

	// And podman is unchanged: the same fixture, the same options, one runtime over,
	// still gets the broker variable. Without this row the assertion above passes for a
	// fix that simply stopped emitting the variable everywhere.
	podmanArgs := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", cfg)
	wantBroker := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		hostServiceEndpointPath(broker.BrokerLoopholeName)
	if !containsStr(podmanArgs, wantBroker) {
		t.Fatalf("podman lost the broker endpoint variable: %v", podmanArgs)
	}
}
