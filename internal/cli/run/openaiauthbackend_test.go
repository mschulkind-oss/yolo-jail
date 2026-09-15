package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func openAIAuthFixture(t *testing.T, approved bool) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), openAIAuthBrokerName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"openai-auth-broker","description":"fixture","version":1,` +
		`"default_enabled":true,"transport":"loopback-tls","lifecycle":"spawned",` +
		`"host_daemon":{"cmd":["yolo","internal","daemon","openai-auth-broker","--socket","{socket}"],` +
		`"publishes":"socket","scope":"host"},` +
		`"jail_daemon":{"cmd":["yolo-jaild","openai-auth-adapter"],"restart":"on-failure"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: dir, HostExecApproved: approved}})
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

func TestAppleContainerMountsOnlyActiveOpenAIAuthEndpoint(t *testing.T) {
	openAIAuthFixture(t, true)
	o := goldenOptions("/ws", t.TempDir())
	args := o.hostServicesMountArgs("container", "yolo-ws-auth", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(openAIAuthBrokerName) + "=" +
		hostServiceEndpointPath(openAIAuthBrokerName)
	if !containsStr(args, want) {
		t.Fatalf("Apple Container args omit OpenAI auth endpoint: %v", args)
	}
	if !containsStr(args, paths.JailHostServicesDir+":rw") &&
		!strings.Contains(strings.Join(args, " "), ":"+paths.JailHostServicesDir+":rw") {
		t.Fatalf("Apple Container args omit service directory mount: %v", args)
	}
}

func TestAppleContainerWithholdsUnapprovedOpenAIAuthEndpoint(t *testing.T) {
	openAIAuthFixture(t, false)
	o := goldenOptions("/ws", t.TempDir())
	if args := o.hostServicesMountArgs("container", "yolo-ws-auth", jsonx.NewOrderedMap()); len(args) != 0 {
		t.Fatalf("unapproved OpenAI auth pack crossed into Apple Container: %v", args)
	}
}
