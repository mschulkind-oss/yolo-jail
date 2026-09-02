package run

// packproviderexclusive_test.go is the LAUNCH half of provider-name exclusivity
// (profiles-as-pack-variants.md §4.1, OQ-12): two declarations of one provider name fail
// the launch, before the container exists, naming both.
//
// Why it belongs at this call site rather than only in packload: the collision has NO
// runtime symptom to fall back on. The composed providers table is keyed by name, so the
// second shipper silently replaces the first, and the jail comes up using a provider the
// user never chose — the R1 shape (a collision no runtime error would ever announce), with
// the config-file half swapped for a config-table one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providerPackDir writes a local pack shipping one named provider and returns its root.
func providerPackDir(t *testing.T, name, provider, baseURL string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"provider","name":"` + provider + `",` +
		`"endpoints":{"openai":{"base_url":"` + baseURL + `","wire_api":"openai-chat-completions"}},` +
		`"api_key_env_name":"` + strings.ToUpper(provider) + `_API_KEY"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestStagePacksRefusesProviderNameCollision pins the pre-flight at its real call site —
// the same reason TestStagePacksRefusesConfigSurfaceCollision does: packload.Collisions
// reports the cross-pack case to `pack lint` and `yolo check`, but nothing at launch reads
// it, so deleting the call would leave the launch green and the table wrong.
func TestStagePacksRefusesProviderNameCollision(t *testing.T) {
	home := packHome(t)
	first := providerPackDir(t, "zai-pack", "zai", "https://api.z.ai/api/paas/v4")
	second := providerPackDir(t, "zai-clone", "zai", "https://mirror.example/v4")
	writeUserPacks(t, home, `["file://`+first+`", "file://`+second+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-provider-collide")
	if err == nil {
		t.Fatal("two packs shipping one provider name must fail the launch — the composed " +
			"providers table is keyed by name, so the second would silently replace the first")
	}
	msg := err.Error()
	for _, want := range []string{"zai-pack", "zai-clone", `"zai"`, "sole-owned"} {
		if !strings.Contains(msg, want) {
			t.Errorf("launch refusal missing %q; got:\n%s", want, msg)
		}
	}
}

// One pack shipping the SAME name twice is the self-collision the generic exclusive loop
// cannot see (it skips a group of one). It is refused EARLIER than the pre-flight — the
// host's strict manifest load reports it, with the declaration indexes the author needs —
// and this pins that the refusal still reaches the launch rather than degrading to a
// last-writer-wins table.
func TestStagePacksRefusesProviderNameDeclaredTwice(t *testing.T) {
	home := packHome(t)
	root := filepath.Join(t.TempDir(), "twice")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"twice","contributes":[` +
		`{"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://a.example/v4"}}},` +
		`{"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://b.example/v4"}}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+root+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-provider-self-collide")
	if err == nil || !strings.Contains(err.Error(), `provider "zai" is declared again`) {
		t.Fatalf("one pack shipping a provider name twice must fail the launch, got: %v", err)
	}
}

// The DESIGNED shapes must still launch: two packs shipping two DIFFERENT providers are the
// ordinary multi-provider world, and the shipped set is untouched by the kind.
func TestStagePacksAllowsDistinctProviderNames(t *testing.T) {
	home := packHome(t)
	first := providerPackDir(t, "zai-pack", "zai", "https://api.z.ai/api/paas/v4")
	second := providerPackDir(t, "acme-pack", "acme", "https://api.acme.dev/v4")
	writeUserPacks(t, home, `["file://`+first+`", "file://`+second+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-provider-distinct")
	if err != nil {
		t.Fatalf("two providers with distinct names are not a collision: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded %d packs, want 2", len(loaded))
	}
}
