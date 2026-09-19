package loopholes

// resolvercapabilities_test.go pins the two manifest facts that travel through the
// config.LoopholeResolver seam: `serves` and `default_enabled`.
//
// THEY EXIST FOR A VALIDATOR THAT CANNOT SEE THIS PACKAGE. The ~/.aws grant conflict
// (config/validate_loopholes.go, docs/design/sso-backed-bedrock.md §8) asks whether
// anything in this config ANSWERS the container-credentials protocol, and it must ask
// that of the declared JOB rather than of a loophole name — `if name == "aws-auth"` is
// the switch on a tool name AGENTS.md forbids. internal/config cannot import this
// package (loopholes -> config is the direction the seam runs in), so the declarations
// have to be copied onto LoopholeInfo here.
//
// Nothing else reads them, which is exactly why they need a test: drop either field from
// this copy and every test in internal/config still passes — they inject a fakeResolver
// — while the real rule silently never fires for any config on any machine.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awschain"
)

// writeServingModule writes a loophole module declaring one capability, with
// `default_enabled` set as asked. It is spelled out rather than reusing writeModule
// because both of the fields under test are ones writeModule does not vary.
func writeServingModule(t *testing.T, parent, name, capability string, defaultEnabled bool) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := "false"
	if defaultEnabled {
		enabled = "true"
	}
	body := `{"name":"` + name + `","description":"` + name + ` desc",` +
		`"default_enabled":` + enabled + `,"serves":["` + capability + `"],` +
		`"transport":"none","lifecycle":"external"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestResolverCarriesServesAndTheAuthorsDefault is the seam pin. `default_enabled:false`
// is the shipped `packs/aws-auth` spelling, and it is the value that would be silently
// wrong if this copy took `true`: the validator combines it with the user's own
// `loopholes.<name>.enabled`, so a resolver that reported every loophole as enabled would
// refuse a ~/.aws grant beside a loophole nobody switched on.
func TestResolverCarriesServesAndTheAuthorsDefault(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeServingModule(t, t.TempDir(), "acme-creds",
		awschain.ContainerCredentialsCapability, false)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	known, ok := NewResolver().Known()
	if !ok {
		t.Fatal("Known() must never report failure — resolver.go's invariant")
	}
	info, isKnown := known["acme-creds"]
	if !isKnown {
		t.Fatal("the resolver did not see the recorded pack module at all")
	}
	if len(info.Serves) != 1 || info.Serves[0] != awschain.ContainerCredentialsCapability {
		t.Errorf("LoopholeInfo.Serves = %v, want [%s] — without it the ~/.aws grant conflict "+
			"never fires on any real machine, and every unit test of it still passes because "+
			"they inject their own resolver", info.Serves, awschain.ContainerCredentialsCapability)
	}
	if info.DefaultEnabled {
		t.Error("LoopholeInfo.DefaultEnabled = true for a manifest that says false — the " +
			"author's default is half of the resolved answer, and getting it backwards " +
			"refuses launches over a loophole that never runs")
	}
}

// TestResolverDefaultEnabledIsTheManifestsAndNotTheConfigs. Known() runs Discover with
// NO config, so the value it copies is the manifest's and only the manifest's. The user's
// switch is applied one layer up, by config.LoopholeEnabledOverride — the same function
// applyWorkspaceOverrides resolves a launch with. A resolver that tried to fold the config
// in here would give the same name two homes.
func TestResolverDefaultEnabledIsTheManifestsAndNotTheConfigs(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeServingModule(t, t.TempDir(), "acme-creds",
		awschain.ContainerCredentialsCapability, true)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	known, _ := NewResolver().Known()
	if !known["acme-creds"].DefaultEnabled {
		t.Error("a manifest declaring default_enabled:true reached the seam as false")
	}
}

// TestResolverServesSilenceIsNotAClaim: a manifest that declares nothing must arrive with
// an empty list, not a populated one. It is loopholedecl's own rule (silence never reads
// as a default claim) restated at the seam, because a resolver that invented a capability
// would make the grant conflict fire for every loophole on the machine.
func TestResolverServesSilenceIsNotAClaim(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	mod := writeModule(t, t.TempDir(), "acme-quiet", nil)
	SetPackModules([]PackModule{{Dir: mod, HostExecApproved: true}})

	known, _ := NewResolver().Known()
	if got := known["acme-quiet"].Serves; len(got) != 0 {
		t.Errorf("LoopholeInfo.Serves = %v for a manifest that declares none", got)
	}
}
