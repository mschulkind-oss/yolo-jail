package wirebridged

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Part 5's model allowlist (allowlist.go; docs/design/wire-bridge-gateway.md §6, WG-I40 to
// WG-I43), through the PRODUCTION boot: shipped packs and a company pack narrowing a list,
// composed and resolved as a launch does; the tables handed to servePlan the way the per-entry
// channel crosses them; and a staged pack tree at YOLO_PACK_ROOT, which the boot reads for which
// agents send models off the list. No request leaves the process.

// companyPack writes a company pack whose contributions are contributes, as a configured pack is
// staged.
func companyPack(t *testing.T, contributes string) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"name":"company","description":"d","contributes":[`+contributes+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(dir, "company")
	if p == nil || len(problems) != 0 {
		t.Fatalf("the company fixture did not load: %v", problems)
	}
	return p
}

// stagedTree stages packs the way a launch's tree holds them (<root>/_official/<name>), each
// pack's manifest copied, and returns the root the boot reads as YOLO_PACK_ROOT.
func stagedTree(t *testing.T, packs []*packload.Pack) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range packs {
		raw, err := os.ReadFile(filepath.Join(p.Root, "pack.json"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "_official", p.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// narrowedBoot is one launch's boot: the named shipped packs and company, composed and resolved
// with user profiles, the plan for use, each agent's env file holding its keys, served on free
// loopback ports. withTree false delivers no pack tree.
type narrowedBoot struct {
	up           *captureUpstream
	adapter, via string
	logs         func() string
	providers    *jsonx.OrderedMap
}

func bootNarrowed(t *testing.T, names []string, company string, user map[string]packload.UserProfile,
	use map[string]string, keys map[string]string, withTree bool) narrowedBoot {
	t.Helper()
	clearAWS(t)
	for _, v := range []string{"ZAI_API_KEY", "CEREBRAS_API_KEY"} {
		t.Setenv(v, "")
	}
	var b narrowedBoot
	b.logs = captureDiag(t)
	b.up = withUpstream(t)
	var packs []*packload.Pack
	for _, p := range packload.Embedded() {
		for _, n := range names {
			if p.Name == n {
				packs = append(packs, p)
			}
		}
	}
	packs = append(packs, companyPack(t, company))
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, user, providers)
	if err != nil {
		t.Fatal(err)
	}
	b.providers = providers
	p := planFor(providers, use, resolved)
	home := t.TempDir()
	for agent, lines := range keys {
		writeAgentKey(t, home, agent, lines)
	}
	if p.adapter != nil {
		b.adapter = freeLoopback(t)
		p.adapter.ListenAddr = b.adapter
	}
	if len(p.via.Routes) > 0 {
		b.via = freeLoopback(t)
		p.via.ListenAddr = b.via
	}
	wire := func(v any) string {
		s, err := jsonx.DumpsCompact(v)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	useJSON, _ := json.Marshal(use)
	vars := map[string]string{"JAIL_HOME": home,
		entrypoint.ProvidersWireEnv:   wire(providers),
		entrypoint.ProfilesWireEnv:    wire(packload.ProfilesWireTable(resolved)),
		entrypoint.UseProfilesWireEnv: string(useJSON)}
	if withTree {
		vars["YOLO_PACK_ROOT"] = stagedTree(t, packs)
	}
	noReadinessPipe(t)
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- servePlan(ctx, p, tokenEnv(vars)) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool {
		raw, err := os.ReadFile(endpointFile)
		return err == nil && strings.TrimSpace(string(raw)) != ""
	})
	return b
}

// zaiOnly narrows zai's shipped list to two of its three models.
const zaiOnly = `{"kind":"models","provider":"zai","only":["glm-5.3","glm-4.6"]}`

func zaiVia(profile string) map[string]packload.UserProfile {
	return map[string]packload.UserProfile{profile: {Provider: "zai", Via: ServiceName}}
}

const zaiKey = "export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}"

// TestAViaRouteRefusesAModelOffANarrowedList is the part's headline: pi on a via route over a
// provider whose list a company pack narrowed is refused a model off it, in OpenAI's shape and
// naming the list and the switch, and nothing is sent upstream; a listed model passes through; a
// request that names no model (GET /models) is not the list's. It fails if the boot stops
// computing the allowlist, or the pass-through stops asking it.
func TestAViaRouteRefusesAModelOffANarrowedList(t *testing.T) {
	b := bootNarrowed(t, []string{"pi", "zai", "wire-bridge"}, zaiOnly, zaiVia("pz"),
		map[string]string{"pi": "pz"}, map[string]string{"pi": zaiKey}, true)
	resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"glm-5.3-flash","messages":[]}`, nil)
	if resp.StatusCode != 400 || b.up.calls() != 0 {
		t.Fatalf("an off-list model: %d, upstream calls %d, want a 400 and none: %s", resp.StatusCode, b.up.calls(), body)
	}
	var doc struct {
		Error struct{ Message, Type string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Error.Type != "invalid_request_error" {
		t.Errorf("the refusal is not OpenAI-shaped: %s", body)
	}
	for _, want := range []string{`model "glm-5.3-flash" is not on provider zai's list`, `profile "pz"`,
		"Allowed: glm-4.6, glm-5.3", `"enforce_models": false`} {
		if !strings.Contains(doc.Error.Message, want) {
			t.Errorf("the refusal lacks %q: %s", want, doc.Error.Message)
		}
	}
	if resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"glm-5.3","messages":[]}`, nil); resp.StatusCode != 200 || b.up.calls() != 1 {
		t.Errorf("a listed model: %d, upstream calls %d: %s", resp.StatusCode, b.up.calls(), body)
	}
	if resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions",
		`{"model":"glm-5.3","messages":[],"model":"glm-5.3-flash"}`, nil); resp.StatusCode != 400 || b.up.calls() != 1 {
		t.Errorf("a body naming model twice: %d, upstream calls %d, want a 400: %s", resp.StatusCode, b.up.calls(), body)
	}
	wantLines(t, b.logs(), "a model off provider zai's list (2 models) is refused",
		`via route for pi: refused model "glm-5.3-flash"`)
}

// TestTheSwitchOffAndAListNoOnlyNarrowedRefuseNothing: the profile's enforce_models off leaves the
// list shaping the menus alone (MM-D5), and a list no `only` narrowed is no allowlist (OQ-MM3 is
// open).
func TestTheSwitchOffAndAListNoOnlyNarrowedRefuseNothing(t *testing.T) {
	off := false
	b := bootNarrowed(t, []string{"pi", "zai", "wire-bridge"}, zaiOnly,
		map[string]packload.UserProfile{"pz": {Provider: "zai", Via: ServiceName, EnforceModels: &off}},
		map[string]string{"pi": "pz"}, map[string]string{"pi": zaiKey}, true)
	if resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"glm-5.3-flash","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Errorf("enforce_models off still refused: %d %s", resp.StatusCode, body)
	}
	b = bootNarrowed(t, []string{"pi", "zai", "wire-bridge"}, `{"kind":"models","provider":"zai","add":[{"id":"glm-x","vendor":"zhipu"}]}`,
		zaiVia("pz"), map[string]string{"pi": "pz"}, map[string]string{"pi": zaiKey}, true)
	if resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"anything","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Errorf("a list no only narrowed refused: %d %s", resp.StatusCode, body)
	}
}

// TestAnAgentWhoseBackgroundModelsAreOffTheListIsAdmitted (WG-I41): codex's pack declares
// unlisted_background_models, so its via route admits an off-list model and logs it. Without the
// staged pack tree the boot cannot tell who that is, and refuses no model on any route (WG-I42).
func TestAnAgentWhoseBackgroundModelsAreOffTheListIsAdmitted(t *testing.T) {
	b := bootNarrowed(t, []string{"codex", "pi", "zai", "wire-bridge"}, zaiOnly, zaiVia("pz"),
		map[string]string{"codex": "pz", "pi": "pz"}, map[string]string{"codex": zaiKey, "pi": zaiKey}, true)
	if resp, body := postTo(t, "http://"+b.via+"/agent/codex/chat/completions", `{"model":"codex-auto-review","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Errorf("codex's off-list model was refused: %d %s", resp.StatusCode, body)
	}
	if resp, _ := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"codex-auto-review","messages":[]}`, nil); resp.StatusCode != 400 {
		t.Errorf("pi's off-list model was admitted: %d", resp.StatusCode)
	}
	wantLines(t, b.logs(), `via route for codex: admitted model "codex-auto-review"`, "codex's pack declares unlisted_background_models")

	b = bootNarrowed(t, []string{"pi", "zai", "wire-bridge"}, zaiOnly, zaiVia("pz"),
		map[string]string{"pi": "pz"}, map[string]string{"pi": zaiKey}, false)
	if resp, body := postTo(t, "http://"+b.via+"/agent/pi/chat/completions", `{"model":"glm-5.3-flash","messages":[]}`, nil); resp.StatusCode != 200 {
		t.Errorf("with no pack tree the bridge still refused: %d %s", resp.StatusCode, body)
	}
	wantLines(t, b.logs(), "refuses no model on any route (wire-bridge-gateway.md WG-I42)")
}

// cerebrasOnly narrows cerebras's list, which the bridge's adapter route carries to claude and
// copilot, to its default and one added model.
const cerebrasOnly = `{"kind":"models","provider":"cerebras","add":[{"id":"qwen-extra","vendor":"qwen"},{"id":"qwen-other","vendor":"qwen"}]},
  {"kind":"models","provider":"cerebras","only":["qwen-3.8-27b","qwen-extra"]}`

const cerebrasKey = "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'cb-key'}"

// TestTheAdapterRouteRefusesOnlyWhatEverySharerWouldBeRefused (WG-I40): claude on a narrowed
// bridged provider is refused an off-list model in Anthropic's shape, before either upstream; a
// listed one is translated as before. When copilot, whose background ids are unmeasured, shares
// the route, the bridge cannot tell their requests apart and refuses nothing.
func TestTheAdapterRouteRefusesOnlyWhatEverySharerWouldBeRefused(t *testing.T) {
	b := bootNarrowed(t, []string{"claude", "cerebras", "wire-bridge"}, cerebrasOnly, nil,
		map[string]string{"claude": "cerebras"}, map[string]string{"claude": cerebrasKey}, true)
	msg := func(model string) string {
		return `{"model":"` + model + `","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	}
	resp, body := postTo(t, "http://"+b.adapter+"/v1/messages", msg("qwen-other"), nil)
	if resp.StatusCode != 400 || b.up.calls() != 0 || !strings.Contains(body, `"type":"error"`) ||
		!strings.Contains(body, `model \"qwen-other\" is not on provider cerebras's list`) {
		t.Fatalf("claude's off-list model: %d, upstream calls %d, want an Anthropic 400: %s", resp.StatusCode, b.up.calls(), body)
	}
	if resp, body := postTo(t, "http://"+b.adapter+"/v1/messages", msg("qwen-extra"), nil); resp.StatusCode != 200 || b.up.calls() != 1 {
		t.Errorf("claude's listed model: %d, upstream calls %d: %s", resp.StatusCode, b.up.calls(), body)
	}

	shared := bootNarrowed(t, []string{"claude", "copilot", "cerebras", "wire-bridge"}, cerebrasOnly, nil,
		map[string]string{"claude": "cerebras", "copilot": "cerebras"},
		map[string]string{"claude": cerebrasKey, "copilot": cerebrasKey}, true)
	if resp, body := postTo(t, "http://"+shared.adapter+"/v1/messages", msg("qwen-other"), nil); resp.StatusCode != 200 {
		t.Errorf("the route copilot shares refused an off-list model: %d %s", resp.StatusCode, body)
	}
	wantLines(t, shared.logs(), "copilot sends models off it, so an off-list model is logged, not refused")
}
