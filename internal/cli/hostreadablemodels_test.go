package cli

// hostreadablemodels_test.go pins the host notch's CALL SITE for a model said once
// (docs/reference/config/rulings.md#RC-D29, docs/reference/config/user-config.md#lines-you-never-write C7): composedHostProviders
// hands the composer the offline catalog, as the jail notch's composedProviders does, so a row
// named without facts gets the catalog's at `yolo host` too.

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

func TestHostComposedProvidersFillsAReadableRowFromTheCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	record, err := json.Marshal(hostfloor.Record{Schema: 1, Bin: "pi", Pack: "fixture", Via: "npm", Declared: "pi@1",
		Version: "1", Dir: installDir, Entry: entry, Exec: []string{entry}})
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		entry:                                "#!/bin/sh\n",
		filepath.Join(floorDir, "bin", "pi"): "#!/bin/sh\n",
		filepath.Join(packageDir, "package.json"):     `{"name":"pi","version":"1"}`,
		filepath.Join(packageDir, "models.json"):      `{"models":[{"id":"acme/big","name":"Big From Catalog","context":400000}]}`,
		filepath.Join(floorDir, "records", "pi.json"): string(record),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	decoded, err := jsonx.Decode([]byte(`{
  "providers": {"acme": {"api_key_env_name": "ACME_API_KEY",
    "endpoints": {"openai": {"base_url": "https://acme.example/v1"}},
    "every_model": {"max_tokens": 32000},
    "models": {"acme/big": {}}}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	providers, _, err := composedHostProviders(decoded.(*jsonx.OrderedMap), []*packload.Pack{pack}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := jsonx.DumpsCompact(providers)
	for _, want := range []string{`"acme/big": "acme/big"`, `"name": "Big From Catalog"`, `"context_window": "400000"`, `"max_tokens": "32000"`} {
		if !strings.Contains(s, want) {
			t.Errorf("composed table = %s\nwant a substring %s", s, want)
		}
	}
}
