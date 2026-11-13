package run

// readablemodels_test.go pins the jail notch's CALL SITE for a model said once
// (docs/reference/config/rulings.md#RC-D29, docs/reference/config/user-config.md#lines-you-never-write C7): composedProviders,
// whose table crosses into the jail as YOLO_PROVIDERS, lowers a provider's readable rows and
// `every_model` and hands the composer the offline catalog, so a row named without facts launches
// with the catalog's. Delete the WithOfflineModelCatalog argument and the catalog's name and
// window never arrive.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// catalogFixturePack is a pack whose program ships an offline model catalog, provisioned on a
// host floor under the test's HOME, as config's explicitProviderCatalog tests stage one.
func catalogFixturePack(t *testing.T, models string) *packload.Pack {
	t.Helper()
	manifest := fstest.MapFS{
		"fixture/pack.json": {Data: []byte(`{"name":"fixture","contributes":[{"kind":"program","bin":"pi","via":"npm","package":"pi@1","model_catalog":["models.json"]}]}`)},
	}
	pack, problems, err := packload.ReadSelectedPackFS(manifest, "fixture", "/diagnostic/fixture", "fixture", nil, nil)
	if err != nil || len(problems) != 0 {
		t.Fatalf("read catalog fixture pack: problems=%v err=%v", problems, err)
	}
	floorDir := paths.HostFloorDir()
	installDir := filepath.Join(floorDir, "programs", "pi")
	packageDir := filepath.Join(installDir, "npm", "lib", "node_modules", "pi")
	entry := filepath.Join(packageDir, "bin", "pi")
	files := map[string]string{
		entry:                                "#!/bin/sh\n",
		filepath.Join(floorDir, "bin", "pi"): "#!/bin/sh\n",
		filepath.Join(packageDir, "package.json"): `{"name":"pi","version":"1"}`,
		filepath.Join(packageDir, "models.json"):  models,
	}
	record, err := json.Marshal(hostfloor.Record{Schema: 1, Bin: "pi", Pack: "fixture", Via: "npm", Declared: "pi@1",
		Version: "1", Dir: installDir, Entry: entry, Exec: []string{entry}})
	if err != nil {
		t.Fatal(err)
	}
	files[filepath.Join(floorDir, "records", "pi.json")] = string(record)
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return pack
}

func TestComposedProvidersLowersReadableRowsWithTheCatalogsFacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pack := catalogFixturePack(t, `{"models":[{"id":"acme/big","name":"Big From Catalog","context":400000}]}`)
	decoded, err := jsonx.Decode([]byte(`{
  "providers": {"acme": {"api_key_env_name": "ACME_API_KEY",
    "endpoints": {"openai": {"base_url": "https://acme.example/v1"}},
    "every_model": {"max_tokens": 32000},
    "models": {"acme/big": {}, "acme/big-200k": {"twin_of": "acme/big", "compact_at": 200000}, "old": "acme-old"}}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	providers, _, err := composedProviders(decoded.(*jsonx.OrderedMap), []*packload.Pack{pack}, packload.NothingServed())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := jsonx.DumpsCompact(providers)
	for _, want := range []string{
		`"acme/big": "acme/big"`,
		`"name": "Big From Catalog"`,
		`"context_window": "400000"`,
		`"wire_id": "acme/big"`,
		`"context_window": "200000"`,
		`"old": {"max_tokens": "32000"}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("composed table = %s\nwant a substring %s", s, want)
		}
	}
}
