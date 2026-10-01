package packs_test

// aws_auth_pointer_test.go pins the one fact packs/aws-auth used to state in two files that
// could not check each other: THE PORT.
//
// The loophole manifest starts the adapter (`yolo-jaild aws-credential-adapter --listen …`)
// and the pack's `env` contribution in [`aws-auth/pack.json`](./aws-auth/pack.json) tells
// every AWS SDK in the jail where to find it (AWS_CONTAINER_CREDENTIALS_FULL_URI). A drifted
// pair produced a jail whose adapter was up, whose pointer was set, and whose every credential
// fetch was a connection refused three retries deep inside somebody's SDK. Since
// docs/plans/notch-convergence.md NC-D41 the port is written ONCE, as the manifest's
// `jail_daemon.listen`, and both the argv and the pointer name it as `{listen}`, which the
// launcher resolves to one served address. What is pinned here is that shape, against
// internal/awscredadapter's own constants, so the third copy cannot drift from the first either.
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

// TestAWSAuthPointerAndAdapterAgreeOnThePort is the drift guard described above: the pointer
// composes its address from the adapter it is served by, and the adapter declares that address
// once, where its argv takes it.
func TestAWSAuthPointerAndAdapterAgreeOnThePort(t *testing.T) {
	wantURI := "http://" + loopholedecl.TokenListen + awscredadapter.CredentialsPath

	gated := awsAuthPack(t).GatedEnvContributions()
	if len(gated) != 1 {
		t.Fatalf("gated env contributions = %+v, want exactly one", gated)
	}
	if got := gated[0].Vars["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; got != wantURI {
		t.Errorf("AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want %q — the pointer composes the "+
			"adapter's served address rather than spelling a port of its own", got, wantURI)
	}
	if gated[0].ServedBy != "aws-auth" {
		t.Errorf("the pointer is served_by %q, want \"aws-auth\" — {listen} resolves to the "+
			"served_by daemon's address", gated[0].ServedBy)
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
	if manifest.JailDaemon.Listen != awscredadapter.DefaultListen {
		t.Errorf("jail_daemon.listen = %q, want %q, the adapter's own default",
			manifest.JailDaemon.Listen, awscredadapter.DefaultListen)
	}
	if !containsArg(manifest.JailDaemon.Cmd, loopholedecl.TokenListen) {
		t.Errorf("jail_daemon cmd = %v, want it to take its address as %s",
			manifest.JailDaemon.Cmd, loopholedecl.TokenListen)
	}
}

// TestEveryShippedListenPointerNamesADaemonThatDeclaresOne generalizes the guard above to every
// shipped pack: a pack env value naming {listen} is withheld at every launch when the daemon it
// is served_by declares no `jail_daemon.listen` (packload's servedFold), so such a pair is a
// pointer that never arrives. codex's refresh URL, served by openai-auth's loophole, is the
// other shipped one.
func TestEveryShippedListenPointerNamesADaemonThatDeclaresOne(t *testing.T) {
	listens := map[string]string{}
	manifests, err := fs.Glob(packs.FS, "*/loopholes/*/"+loopholedecl.ManifestName)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range manifests {
		m, err := loopholedecl.Decode(mustRead(t, path), "/"+strings.TrimSuffix(path, "/"+loopholedecl.ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		if m.JailDaemon != nil && m.JailDaemon.Listen != "" {
			listens[m.Name] = m.JailDaemon.Listen
		}
	}
	packFiles, err := fs.Glob(packs.FS, "*/"+packdecl.ManifestName)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range packFiles {
		m, problems := packdecl.Decode(mustRead(t, path))
		if len(problems) != 0 {
			t.Fatalf("%s: %v", path, problems)
		}
		check := func(vars map[string]string, servedBy string) {
			for k, v := range vars {
				if !strings.Contains(v, loopholedecl.TokenListen) {
					continue
				}
				seen[k] = true
				if listens[servedBy] == "" {
					t.Errorf("%s: %s=%q names %s, but its daemon %q declares no jail_daemon.listen",
						path, k, v, loopholedecl.TokenListen, servedBy)
				}
			}
		}
		servedBy := m.EnvServedBy()
		for k, v := range m.EnvContributions() {
			check(map[string]string{k: v}, servedBy[k])
		}
		for _, g := range m.GatedEnvContributions() {
			check(g.Vars, g.ServedBy)
		}
	}
	for _, want := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "CODEX_REFRESH_TOKEN_URL_OVERRIDE"} {
		if !seen[want] {
			t.Errorf("%s no longer names %s, so this guard checks nothing for it", want, loopholedecl.TokenListen)
		}
	}
}

// TestAWSAuthPointerIsPlatformGated: selecting this pack must change NOTHING observable
// until an agent is actually pointed at Bedrock
// ([sso-backed-bedrock.md §12](../docs/design/sso-backed-bedrock.md#12-what-i-would-build-in-order)
// step 4).
//
// The gate is the PROVIDER'S PLATFORM, "aws-bedrock"
// ([`OQ-BR8`](../docs/design/providers-and-profiles-redesign.md#OQ-BR8), ruled 2026-09-29,
// with [`OQ-BR2`](../docs/design/providers-and-profiles-redesign.md#OQ-BR2)'s marker), which is
// what lets one pointer serve every consumer and every name: `-p bedrock`, a user's own profile
// over that provider, and a user's own provider declaring the platform. It was the profile NAME
// `bedrock`, which a second profile over the same provider lost (trap D5). An UNGATED
// contribution would set the variable in every jail that selects this pack — including one
// whose loophole is disabled for want of a Bedrock selection — and an AWS SDK would then dial a
// port with nothing behind it instead of falling through its chain. Per agent since the
// credential gate ([`OQ-BR4`](../docs/reference/providers.md#oq-br4)).
func TestAWSAuthPointerIsPlatformGated(t *testing.T) {
	m := awsAuthPack(t)
	if unconditional := m.EnvContributions(); len(unconditional) != 0 {
		t.Errorf("ungated env contributions = %v, want none — selecting aws-auth must change "+
			"nothing observable until an agent is pointed at Bedrock", unconditional)
	}
	gated := m.GatedEnvContributions()
	if len(gated) != 1 || gated[0].Platform != "aws-bedrock" || gated[0].Profile != "" {
		t.Fatalf("gated env = %+v, want exactly one, gated on the platform `aws-bedrock` and on no "+
			"profile name", gated)
	}
}

// TestAWSAuthPackCarriesNoCredentialVariable. The pack's whole point is that no
// credential crosses into the jail, and an env contribution is the one declaration in
// this file that COULD carry one — a var is inherited by every process the agent
// spawns, so a value here would be readable by every MCP server and every shell in
// the jail, permanently, with no expiry.
//
// AWS_CONTAINER_AUTHORIZATION_TOKEN is the protocol's request header carried by value, and the
// manifest must never hold a secret: it names the adapter's caller token as the TOKEN
// `{caller_token}`, which the launch resolves to the per-launch value and delivers only in the
// env file of an agent whose profile selects `bedrock` (docs/reference/providers.md
// OQ-CN7 (c)). It used to name the boot's token FILE instead, which every jail process could be
// pointed at; and AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE must stay absent, because the SDKs
// prefer the file to the token when both are set. Both are pinned below.
func TestAWSAuthPackCarriesNoCredentialVariable(t *testing.T) {
	forbidden := []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK",
	}
	m := awsAuthPack(t)
	vars := map[string]string{}
	for k, v := range m.EnvContributions() {
		vars[k] = v
	}
	for _, gated := range m.GatedEnvContributions() {
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
	// The token travels as the scoped `{caller_token}`, never a literal and never by file.
	if got, want := vars["AWS_CONTAINER_AUTHORIZATION_TOKEN"], "{caller_token}"; got != want {
		t.Errorf("AWS_CONTAINER_AUTHORIZATION_TOKEN = %q, want %q — without it the SDK "+
			"sends no Authorization and the adapter refuses every credential fetch", got, want)
	}
	if v, ok := vars["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"]; ok {
		t.Errorf("the pack declares AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE=%q, which the SDK "+
			"prefers over the scoped token", v)
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
