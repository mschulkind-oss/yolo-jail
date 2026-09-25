package packs_test

// aws_auth_pointer_test.go pins the one fact packs/aws-auth states in two files that
// cannot check each other: THE PORT.
//
// The loophole manifest starts the adapter (`yolo-jaild aws-credential-adapter
// --listen …`) and the pack's `env` contribution in
// [`aws-auth/pack.json`](./aws-auth/pack.json) tells every AWS SDK in the jail
// where to find it (AWS_CONTAINER_CREDENTIALS_FULL_URI). Nothing at runtime compares
// them: a drifted pair produces a jail whose adapter is up, whose pointer is set, and
// whose every credential fetch is a connection refused three retries deep inside
// somebody's SDK. Both are pinned here against internal/awscredadapter's own
// constants, so the third copy cannot drift from the first two either.
//
// It lives in package packs because that is where the embed is, and reading the
// EMBED rather than the tree is the same choice internal/loopholedecl's census makes:
// an installed binary carries the packs and no checkout, so a test that walked
// packs/ would say nothing about what ships.

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func awsAuthPack(t *testing.T) *packdecl.Manifest {
	t.Helper()
	data, err := fs.ReadFile(packs.FS, "aws-auth/"+packdecl.ManifestName)
	if err != nil {
		t.Fatalf("reading the embedded aws-auth pack: %v", err)
	}
	m, problems := packdecl.Decode(data)
	if len(problems) != 0 {
		t.Fatalf("aws-auth/pack.json: %v", problems)
	}
	return m
}

// TestAWSAuthPointerAndAdapterAgreeOnThePort is the drift guard described above.
func TestAWSAuthPointerAndAdapterAgreeOnThePort(t *testing.T) {
	wantURI := "http://" + awscredadapter.DefaultListen + awscredadapter.CredentialsPath

	gated := awsAuthPack(t).ProfiledEnvContributions()
	if len(gated) != 1 {
		t.Fatalf("profiled env contributions = %+v, want exactly one", gated)
	}
	if got := gated[0].Vars["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; got != wantURI {
		t.Errorf("AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want %q — the pointer and the "+
			"adapter's listen address are one fact in two files, and a drifted pair is a "+
			"connection refused inside an SDK rather than an error anyone reports", got, wantURI)
	}

	manifest, err := loopholedecl.Decode(
		mustRead(t, "aws-auth/loopholes/aws-auth/"+loopholedecl.ManifestName), "/loopholes/aws-auth")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.JailDaemon == nil {
		t.Fatal("the aws-auth manifest declares no jail_daemon, so nothing binds the port " +
			"the pointer names")
	}
	if !containsArg(manifest.JailDaemon.Cmd, awscredadapter.DefaultListen) {
		t.Errorf("jail_daemon cmd = %v, want it to bind %q — the pointer above names it",
			manifest.JailDaemon.Cmd, awscredadapter.DefaultListen)
	}
}

// TestAWSAuthPointerIsProfileGated: selecting this pack must change NOTHING observable
// until an agent is actually pointed at Bedrock
// ([sso-backed-bedrock.md §12](../docs/design/sso-backed-bedrock.md#12-what-i-would-build-in-order)
// step 4).
//
// The gate is a profile NAME rather than a pack name, which is what lets one pointer
// serve every consumer: `bedrock` is packs/claude's profile today, and the other three
// agents' land with docs/design/bedrock-plumbing.md. An UNGATED contribution would set
// the variable in every jail that selects this pack — including one whose loophole is
// disabled for want of a profile — and an AWS SDK would then dial a port with nothing
// behind it instead of falling through its chain.
//
// Neither half of that gate is final. Whether a gate keys on the profile NAME or on
// the provider is still open, as
// [`OQ-BR8`](../docs/design/providers-and-profiles-redesign.md#OQ-BR8). And the gate's
// jail-wide reach — the variable lands for every agent in the jail once any one selects
// `bedrock` — was ruled against on 2026-09-25
// ([`OQ-BR4`](../docs/design/provider-credential-scope.md#7-decision-ledger): per-agent
// delivery, not yet built). This test pins today's behavior until those land.
func TestAWSAuthPointerIsProfileGated(t *testing.T) {
	m := awsAuthPack(t)
	if unconditional := m.EnvContributions(); len(unconditional) != 0 {
		t.Errorf("ungated env contributions = %v, want none — selecting aws-auth must change "+
			"nothing observable until an agent is pointed at Bedrock", unconditional)
	}
	gated := m.ProfiledEnvContributions()
	if len(gated) != 1 || gated[0].Profile != "bedrock" {
		t.Fatalf("profiled env = %+v, want exactly one gated on the `bedrock` profile", gated)
	}
}

// TestAWSAuthPackCarriesNoCredentialVariable. The pack's whole point is that no
// credential crosses into the jail, and an env contribution is the one declaration in
// this file that COULD carry one — a var is inherited by every process the agent
// spawns, so a value here would be readable by every MCP server and every shell in
// the jail, permanently, with no expiry.
//
// AWS_CONTAINER_AUTHORIZATION_TOKEN is in the list for a different reason and is worth
// reading twice: it is not a credential, it is the protocol's optional request header,
// and it is absent DELIBERATELY
// ([sso-backed-bedrock.md §5](../docs/design/sso-backed-bedrock.md#5-the-recommended-shape)). Setting it would buy nothing — everything
// that could read the variable can already reach the port — while implying a boundary
// that is not there. The real boundary is the 0600 endpoint file on the hop the adapter
// makes, not a header on the hop it serves.
func TestAWSAuthPackCarriesNoCredentialVariable(t *testing.T) {
	forbidden := []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
	}
	m := awsAuthPack(t)
	vars := map[string]string{}
	for k, v := range m.EnvContributions() {
		vars[k] = v
	}
	for _, gated := range m.ProfiledEnvContributions() {
		for k, v := range gated.Vars {
			vars[k] = v
		}
	}
	for _, name := range forbidden {
		if value, ok := vars[name]; ok {
			t.Errorf("the pack declares %s=%q; no credential and no authorization token may "+
				"travel in this pack's environment", name, value)
		}
	}
	// AWS_REGION belongs to the PROVIDER entry, which already writes it. A second
	// writer of one fact is how the Bedrock region became confusing in the first place,
	// and the container-credentials response has no field for a region at all.
	if _, ok := vars["AWS_REGION"]; ok {
		t.Error("the pack declares AWS_REGION; the provider entry owns that fact and a " +
			"second writer of it is what this design deliberately avoids")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := fs.ReadFile(packs.FS, path)
	if err != nil {
		t.Fatalf("reading %s from the pack embed: %v", path, err)
	}
	return data
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want || strings.Contains(a, want) {
			return true
		}
	}
	return false
}
