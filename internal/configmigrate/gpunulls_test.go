package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gpuRead is what a launch makes of the merged config's GPU section: each leaf as the launch's
// readers resolve it, a null or absent leaf as its default (config.EffectiveGPU).
func gpuRead(cfg *jsonx.OrderedMap) string {
	return jsonx.MustDumpsCompact(config.EffectiveGPU(cfg))
}

// A legacy null under `gpu` means what the leaf's absence means: the default the launch reads
// (config.GPUNullDefaults). Over a lower layer's value it reset that value, which TOML says by
// writing the default; where no other file writes the leaf, leaving it out says the same. In every
// shape — the owner's (a project capability with a local null, 2026-11-09), every sibling leaf, the
// whole section, one vendor's leaves under the other vendor, and a user value under a project
// null — the config launches before and after, `yolo config migrate` converts every file, and the
// launch reads the same GPU settings. Fails if the converter drops the null (the lower layer's
// value shows through, "- null + compute,utility"), refuses it, or writes an invalid default.
func TestMigrationKeepsEveryLegacyGPUNull(t *testing.T) {
	for _, mix := range []gapMix{
		{"the owner's: project capabilities, local null", map[string]string{
			"ws/yolo-jail.jsonc":       `{"gpu": {"enabled": true, "capabilities": "compute,utility"}}`,
			"ws/yolo-jail.local.jsonc": `{"gpu": {"capabilities": null}}`,
		}, "", ""},
		{"the owner's forms3: amd local over an nvidia project, capabilities null", map[string]string{
			"ws/yolo-jail.jsonc": `{"gpu": {"enabled": true, "capabilities": "compute,utility,graphics"}}`,
			"ws/yolo-jail.local.jsonc": `{"gpu": {"enabled": true, "capabilities": null, "vendor": "amd",
				"mode": "devices", "devices": "all"}}`,
		}, "", ""},
		{"a non-default project value under a local null", map[string]string{
			"ws/yolo-jail.jsonc":       `{"gpu": {"enabled": true, "capabilities": "utility", "devices": "0,1"}}`,
			"ws/yolo-jail.local.jsonc": `{"gpu": {"capabilities": null, "devices": null}}`,
		}, "", ""},
		{"a null nothing else writes", map[string]string{
			"ws/yolo-jail.jsonc": `{"gpu": {"enabled": true, "capabilities": null, "devices": null, "vendor": null}}`,
		}, "", ""},
		{"every amd leaf reset by a local null, back to nvidia", map[string]string{
			"ws/yolo-jail.jsonc": `{"gpu": {"enabled": true, "vendor": "amd", "mode": "cdi", "devices": "0",
				"hsa_override_gfx_version": "11.0.0", "seccomp_unconfined": true, "vaapi": true}}`,
			"ws/yolo-jail.local.jsonc": `{"gpu": {"vendor": null, "mode": null, "devices": null,
				"hsa_override_gfx_version": null, "seccomp_unconfined": null, "vaapi": null}}`,
		}, "", ""},
		{"an nvidia capability reset under amd", map[string]string{
			"config.jsonc":       `{"gpu": {"capabilities": "compute,utility,video"}}`,
			"ws/yolo-jail.jsonc": `{"gpu": {"enabled": true, "vendor": "amd", "capabilities": null}}`,
		}, "", ""},
		{"enabled reset by a project null", map[string]string{
			"config.jsonc":       `{"gpu": {"enabled": true, "devices": "0"}}`,
			"ws/yolo-jail.jsonc": `{"gpu": {"enabled": null}}`,
		}, "", ""},
		{"the whole section reset by a project null", map[string]string{
			"config.jsonc":       `{"gpu": {"enabled": true, "devices": "0"}}`,
			"ws/yolo-jail.jsonc": `{"gpu": null}`,
		}, "", ""},
		{"the whole section null, nothing beneath", map[string]string{
			"ws/yolo-jail.jsonc": `{"gpu": null, "packages": ["just"]}`,
		}, "", ""},
		{"a legacy null under a TOML user config", map[string]string{
			"config.toml":        "[machine.gpu]\nenabled = true\ncapabilities = 'utility'\n",
			"ws/yolo-jail.jsonc": `{"gpu": {"capabilities": null}}`,
		}, "", ""},
	} {
		t.Run(mix.name, func(t *testing.T) {
			// A filled default and an absent leaf are one fact, and the read compares them
			// as the launch does; the merged configs' spelling may differ.
			assertMigrationKeeps(t, mix, "", gpuRead, false)
		})
	}
}

// The owner's shape converts to exactly what it means: the local file resets the capabilities to
// the default the null meant, and a null nothing else writes is left out, with no empty table
// behind it. Converting does not touch the old file's values (lower once aliased them, so the plan's
// second lowering never saw the null and could not report it).
func TestGPUNullConvertsToTheDefaultItMeant(t *testing.T) {
	env(t)
	ws := realDir(t)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"gpu": {"enabled": true, "capabilities": "utility"}}`)
	write(t, filepath.Join(ws, "yolo-jail.local.jsonc"), `{"gpu": {"capabilities": null, "devices": null}}`)
	p := plan(t, Request{Workspace: ws})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v", p.Differences)
	}
	local := string(target(t, p, filepath.Join(ws, "yolo-jail.local.jsonc")).Bytes)
	if !strings.Contains(local, "capabilities = \""+config.DefaultGPUCapabilities+"\"") {
		t.Errorf("the local file does not reset the capabilities to the default:\n%s", local)
	}
	if strings.Contains(local, "devices") {
		t.Errorf("a null nothing else writes was written:\n%s", local)
	}

	ws2 := realDir(t)
	write(t, filepath.Join(ws2, "yolo-jail.jsonc"), `{"packages": ["just"], "gpu": {"capabilities": null}}`)
	p2 := plan(t, Request{Workspace: ws2})
	if !p2.Equal || len(p2.NotConverted) > 0 {
		t.Fatalf("plan: equal=%v not converted=%+v differences=%+v", p2.Equal, p2.NotConverted, p2.Differences)
	}
	tg := target(t, p2, filepath.Join(ws2, "yolo-jail.jsonc"))
	if got := string(tg.Bytes); strings.Contains(got, "gpu") {
		t.Errorf("a null nothing else writes left a gpu table behind:\n%s", got)
	}
	if len(tg.Inert) != 1 || tg.Inert[0].Path != "gpu.capabilities" {
		t.Errorf("the dropped null is not reported: %+v", tg.Inert)
	}
	if data, _ := os.ReadFile(filepath.Join(ws2, "yolo-jail.jsonc")); !strings.Contains(string(data), `"capabilities": null`) {
		t.Errorf("planning changed the old file: %s", data)
	}
}
