package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestReadableSemanticInputsSeparateDeclaredPackGrantsFromCallCells(t *testing.T) {
	pack := selectedCallBrokerFixturePack(t, t.TempDir(), "", []string{"working_set", "all"})
	file := filepath.Join(pack.Root, "loopholes", "tasks", "loophole.toml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data = append([]byte("access=\"inspect fixture projects\"\n"), data...)
	data = append(data, []byte("\n[[grants]]\nkind=\"inspect\"\ntext=\"inspect fixture projects\"\n")...)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"always", "on_request"} {
		t.Run(field, func(t *testing.T) {
			user := editReadableMap(t, `{"allow":{"local-tasks":{"`+field+`":["inspect","all.rerun_workflows"]}}}`)
			input, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
				Config: user, User: user, Workspace: editReadableMap(t, `{}`),
				Packs: []*packload.Pack{pack}, Catalog: map[string]map[string]packdecl.ProviderModel{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(input.AllowancePlan.Sources) != 1 {
				t.Fatalf("sources=%+v", input.AllowancePlan.Sources)
			}
			policy := input.AllowancePlan.Sources[0]
			cells := policy.Always
			wantCeiling := LoopholeCeilingAlways
			if field == "on_request" {
				cells = policy.OnRequest
				wantCeiling = LoopholeCeilingOnRequest
			}
			if !reflect.DeepEqual(cells, []string{"all.rerun_workflows"}) {
				t.Fatalf("grant became cell ceiling: %+v", policy)
			}
			grants := input.AllowancePlan.LoopholeGrants
			if len(grants.Packs) != 1 || len(grants.Packs[0].Kinds) != 1 || grants.Packs[0].Kinds[0].Grant.Kind != "inspect" || grants.Packs[0].Kinds[0].Ceiling != wantCeiling {
				t.Fatalf("grant reader changed: %+v", grants)
			}
		})
	}
}
