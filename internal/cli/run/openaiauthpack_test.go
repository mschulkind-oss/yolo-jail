package run

// openaiauthpack_test.go pins the declarative seam between the Codex agent pack
// and the shared OpenAI authentication service. The broker implementation lives
// in core, but selecting Codex must be enough to stage the pack that starts it,
// and Codex must send refreshes to the in-jail adapter rather than to OpenAI.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
)

const codexRefreshAdapterURL = "http://127.0.0.1:1460/oauth/token"

func TestCodexPackNeedsSharedOpenAIAuthUnconditionally(t *testing.T) {
	needs := officialPack(t, "codex").Decl.DeclaredNeeds()
	if len(needs) != 1 || needs[0].Pack != "openai-auth" || len(needs[0].WhenBins) != 0 {
		t.Fatalf("codex needs = %v, want one unconditional openai-auth need — selecting the "+
			"agent must also select its one refresh owner", needs)
	}
}

func TestStagePacksJoinsOpenAIAuthForCodex(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["codex"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-openai-auth-joined")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	var names []string
	for _, p := range loaded {
		names = append(names, p.Name)
	}
	if !containsString(names, "openai-auth") {
		t.Fatalf("codex's need did not join openai-auth: loaded = %v", names)
	}
	if got := errBuf.String(); !strings.Contains(got, "+ openai-auth (needed by codex)") {
		t.Errorf("the launch must disclose why the auth pack joined:\n%s", got)
	}
}

// THE PRODUCTION CALL SITE must use stagePacks' completed needs closure when deciding
// whether a loophole owner departed. openai-auth is normally absent from the literal config:
// Codex and Pi pull it in through `needs`. Comparing the ownership record only with the
// literal config archived the canonical broker credentials on every later launch.
func TestStageRunPacksPreservesNeededOpenAIAuthState(t *testing.T) {
	for _, tc := range []struct{ agent, config string }{
		{"codex", `["codex"]`},
		{"pi", `["pi"]`},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			home := retireHome(t)
			writeUserPacks(t, home, tc.config)
			rec := &packstage.LoopholeOwners{Owners: map[string]string{
				"openai-auth-broker": "openai-auth",
			}}
			if err := rec.Save(packLoopholeOwnersPath()); err != nil {
				t.Fatal(err)
			}
			stateDir, _ := writeLoopholeState(t, "openai-auth-broker", "CANONICAL CREDENTIALS")

			var out bytes.Buffer
			o := retireOptions(t, &out)
			if _, ok := o.stageRunPacks("yolo-openai-auth-needs-" + tc.agent); !ok {
				t.Fatalf("stageRunPacks failed:\n%s", out.String())
			}
			if _, err := os.ReadFile(filepath.Join(stateDir, "ca.key")); err != nil {
				t.Fatalf("%s's dependency-selected broker state was archived: %v\n%s",
					tc.agent, err, out.String())
			}
			if strings.Contains(out.String(), "ARCHIVED") {
				t.Errorf("%s launch reported its active broker as retired:\n%s", tc.agent, out.String())
			}
			got, err := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
			if err != nil {
				t.Fatal(err)
			}
			if got.Owners["openai-auth-broker"] != "openai-auth" {
				t.Errorf("broker ownership was lost: %v", got.Owners)
			}
		})
	}
}

func TestCodexRoutesRefreshesToTheJailAdapter(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "codex"), officialPack(t, "openai-auth")}
	la := zaiLaunchAssembled(t, packs, bareConfig(), nil, nil)
	if got := la.channelEnv(t, "CODEX_REFRESH_TOKEN_URL_OVERRIDE"); len(got) != 1 ||
		got[0] != "CODEX_REFRESH_TOKEN_URL_OVERRIDE="+codexRefreshAdapterURL {
		t.Fatalf("Codex refresh route = %q, want the in-jail broker adapter", got)
	}
}

func TestShippedOpenAIAuthPackOwnsOneHostSingletonAndAdapter(t *testing.T) {
	p := officialPack(t, "openai-auth")
	mods := packLoopholeModules([]*packload.Pack{p})
	if len(mods) != 1 {
		t.Fatalf("openai-auth loophole modules = %d, want 1", len(mods))
	}
	lp, err := loopholes.LoadPackLoophole(mods[0].Dir)
	if err != nil {
		t.Fatalf("loading shipped OpenAI auth loophole: %v", err)
	}
	if lp.Name != "openai-auth-broker" || !lp.Enabled {
		t.Errorf("loophole = %q enabled=%v, want openai-auth-broker enabled by pack selection",
			lp.Name, lp.Enabled)
	}
	wantHost := []string{
		"yolo", "internal", "daemon", "openai-auth-broker",
		"--socket", "{socket}", "--state-file", "{state}/credentials.json",
	}
	if lp.HostDaemon == nil || !reflect.DeepEqual(lp.HostDaemon.Cmd, wantHost) ||
		lp.HostDaemon.Scope != "host" || lp.HostDaemon.Publishes != "socket" {
		t.Errorf("host daemon = %+v, want one host-scoped socket publisher %v", lp.HostDaemon, wantHost)
	}
	wantJail := []string{"yolo-jaild", "openai-auth-adapter", "--listen", "127.0.0.1:1460"}
	if lp.JailDaemon == nil || !reflect.DeepEqual(lp.JailDaemon.Cmd, wantJail) ||
		lp.JailDaemon.Restart != "on-failure" {
		t.Errorf("jail daemon = %+v, want adapter %v", lp.JailDaemon, wantJail)
	}
	if !reflect.DeepEqual(lp.StateFiles, []string{"public-status.json"}) {
		t.Errorf("state_files = %v, want only public-status.json; an absent list mounts the "+
			"whole broker state, including the canonical refresh token, into the jail", lp.StateFiles)
	}
	if len(lp.Intercepts) != 0 || lp.HasCA() {
		t.Errorf("OpenAI auth must use the supported refresh override, not TLS interception: "+
			"intercepts=%v ca=%v", lp.Intercepts, lp.HasCA())
	}
}
