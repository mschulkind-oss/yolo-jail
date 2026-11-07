package check

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// RC-D68 at `yolo check`: selecting a provider by the id the user's `name` replaced is predicted
// as the launch refuses it, naming the new name in the owner's words.
func TestCheckRefusesAReplacedProviderNameNamingTheNewOne(t *testing.T) {
	m, problems := packdecl.Decode([]byte(`{"contributes":[
		{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["openai-responses"],"provider_sets":true},
		{"kind":"provider","name":"openrouter","platform":"openrouter"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	packs := []*packload.Pack{{Name: "p", Decl: m}}
	doc, err := configdoc.Parse([]byte(`{"providers": {"openrouter": {"name": "or"}}, "active_provider": {"default": "openrouter"}}`), configdoc.JSONC)
	if err != nil {
		t.Fatal(err)
	}
	cfg := doc.Values
	got := profileDeclarationGap(cfg, func() ([]*packload.Pack, bool) { return packs, true },
		func(selected []*packload.Pack) (map[string]packload.UserProfile, error) {
			return config.SelectableProfiles(map[string]packload.UserProfile{}, cfg, selected)
		})
	want := `openrouter is named "or" in your config (providers.openrouter.name): -p or`
	if !strings.Contains(strings.Join(got, "\n"), want) {
		t.Fatalf("check predicted %q, want it to name %q", got, want)
	}
}
