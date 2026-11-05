package loopholedecl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func writeModule(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const probeTOML = `name = "probe"
transport = "loopback-tls"
version = 1

[settings.second]
type = "int"
scope = "user"
default = 7

[settings.first]
type = "string"
scope = "user"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "probe"]
publishes = "socket"
`

// TestLoopholeTOMLIsRead: loophole.toml is a loophole's manifest, with its settings in written
// order and an integer default an integer.
func TestLoopholeTOMLIsRead(t *testing.T) {
	dir := writeModule(t, "probe", map[string]string{"loophole.toml": probeTOML})
	m, err := loopholedecl.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if m.Name != "probe" || m.HostDaemon == nil || m.HostDaemon.Publishes != loopholedecl.PublishesSocket {
		t.Fatalf("decoded %+v", m)
	}
	if len(m.Settings) != 2 || m.Settings[0].Key != "second" || m.Settings[1].Key != "first" {
		t.Fatalf("settings lost their written order: %+v", m.Settings)
	}
	if got := loopholedecl.ManifestPath(dir); filepath.Base(got) != "loophole.toml" {
		t.Errorf("ManifestPath = %s", got)
	}
}

// TestAModuleHoldingBothManifestsIsRefusedNamingTheFileToDelete is RC-D28's both-files rule, the
// same refusal pack.json beside pack.toml gets (RC-D19): two files that may disagree are not
// read as either, and the message names the legacy one to delete.
func TestAModuleHoldingBothManifestsIsRefusedNamingTheFileToDelete(t *testing.T) {
	dir := writeModule(t, "probe", map[string]string{
		"loophole.toml":  probeTOML,
		"manifest.jsonc": `{"name": "probe"}`,
	})
	for name, load := range map[string]func() error{
		"strict":   func() error { _, err := loopholedecl.LoadDir(dir); return err },
		"tolerant": func() error { _, _, err := loopholedecl.LoadDirTolerant(dir); return err },
	} {
		err := load()
		if err == nil {
			t.Fatalf("%s: a module holding both manifests loaded", name)
		}
		if !strings.Contains(err.Error(), "delete manifest.jsonc") || !strings.Contains(err.Error(), "loophole.toml") {
			t.Errorf("%s: the refusal does not name the file to delete: %v", name, err)
		}
	}
}

// TestALegacyManifestStillLoadsForTheLegacyWindow: a third-party module on manifest.jsonc reads as
// it always did.
func TestALegacyManifestStillLoadsForTheLegacyWindow(t *testing.T) {
	dir := writeModule(t, "probe", map[string]string{"manifest.jsonc": `{
  // legacy
  "name": "probe", "transport": "none"
}`})
	m, err := loopholedecl.LoadDir(dir)
	if err != nil || m.Name != "probe" || m.Transport != loopholedecl.TransportNone {
		t.Fatalf("legacy manifest: %+v, %v", m, err)
	}
}

// TestLoopholeTOMLRefusesAnUnknownKeyStrictlyAndSkipsItTolerantly: the strict and tolerant
// decoders draw the same line in TOML as in JSONC.
func TestLoopholeTOMLRefusesAnUnknownKeyStrictlyAndSkipsItTolerantly(t *testing.T) {
	dir := writeModule(t, "probe", map[string]string{"loophole.toml": probeTOML + "\nhost_deamon = \"typo\"\n"})
	if _, err := loopholedecl.LoadDir(dir); err == nil || !strings.Contains(err.Error(), "host_deamon") {
		t.Errorf("strict read of a typo'd key: %v", err)
	}
	m, skipped, err := loopholedecl.LoadDirTolerant(dir)
	if err != nil || m == nil || len(skipped) == 0 {
		t.Errorf("tolerant read: %v skipped %v", err, skipped)
	}
}

// TestRetiredEnabledKeyIsRefusedInTOMLToo: the retired manifest key `enabled` is refused whatever
// the syntax (the walk is shared), naming the grant declaration.
func TestRetiredEnabledKeyIsRefusedInTOMLToo(t *testing.T) {
	dir := writeModule(t, "probe", map[string]string{"loophole.toml": "name = \"probe\"\nenabled = true\n"})
	if _, _, err := loopholedecl.LoadDirTolerant(dir); err == nil || !strings.Contains(err.Error(), "[[grants]]") {
		t.Fatalf("retired key in TOML: %v", err)
	}
}

func TestCallBrokerHTTPApprovedBindingTOMLDecoder(t *testing.T) {
	const manifest = `name = "tasks"
transport = "loopback-tls"
[call_broker]
source = "local-tasks"
front = "http"
classifier = ["yolo", "internal", "call-broker-classify", "tasks"]
env_from_host = ["TASKS_TOKEN"]
read_over_post = ["/graphql"]
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "project"
[[call_broker.cell]]
name = "update"
tables = ["all"]
[call_broker.http]
origin = "https://fixture.example:443"
request_headers = ["Content-Type"]
[call_broker.http.credential]
source = "host_env"
key = "TASKS_TOKEN"
header = "Authorization"
prefix = "Bearer "
`
	load := func(body string) error {
		dir := writeModule(t, "tasks", map[string]string{"loophole.toml": body})
		m, err := loopholedecl.LoadDir(dir)
		if err == nil && (m.CallBroker == nil || m.CallBroker.HTTP == nil || m.CallBroker.HTTP.Credential.Key != "TASKS_TOKEN" || m.CallBroker.ReadOverPost[0] != "/graphql") {
			t.Fatalf("selected TOML lost HTTP binding: %+v", m.CallBroker)
		}
		return err
	}
	if err := load(manifest); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"unknown", strings.Replace(manifest, "request_headers = [\"Content-Type\"]", "request_headers = [\"Content-Type\"]\nunapproved_header = \"X-Token\"", 1)},
		{"duplicate", strings.Replace(manifest, "origin = \"https://fixture.example:443\"", "origin = \"https://fixture.example:443\"\norigin = \"https://other.example\"", 1)},
		{"wrong type", strings.Replace(manifest, "key = \"TASKS_TOKEN\"", "key = 5", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := load(tc.body); err == nil {
				t.Fatal("invalid TOML HTTP binding loaded")
			}
		})
	}
}
