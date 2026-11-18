package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
)

func TestScopedOperationPolicyPublicReaderProducesGoverningLines(t *testing.T) {
	input, _ := scopedPolicyFixture(t, `packs=["aws"]
[allow.aws]
on_request=["read","all.put_objects"]
[scopes.root]
source="aws"
resource="s3://bkt"
[scopes.root.allow]
always=["read"]
never=["all","working_set"]
`, `[call_roots]
aws=["s3://bkt"]
[scopes.root]
source="aws"
resource="s3://bkt"
[scopes.root.requests]
read=false
`)
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	policy, has := plan.OperationPolicies["aws"]
	if !has {
		t.Fatal("public resolver did not call the operation-policy adapter")
	}
	readCell := ""
	for _, cell := range plan.Sources[0].CallBroker.Cells {
		if cell.IsRead() {
			readCell = cell.Name
			break
		}
	}
	chain := []brokerpolicy.Resource{{Type: "object", ID: "s3://bkt/key"}, {Type: "location", ID: "s3://bkt"}}
	operations := []brokerpolicy.Operation{{Source: "aws", Kind: brokerpolicy.Read, Cell: readCell, Chain: chain}, {Source: "aws", Kind: brokerpolicy.Write, Cell: "put_objects", Chain: chain}}
	got := policy.Decide(operations, "ws", nil)
	if got.Decision != brokerpolicy.Refuse || len(got.Operations) != 2 {
		t.Fatalf("strict bundle not produced: %+v", got)
	}
	read, write := got.Operations[0], got.Operations[1]
	if read.Ask.Value != brokerpolicy.False || read.Ceiling.Value != brokerpolicy.Always || write.Ceiling.Value != brokerpolicy.Never || read.Table != "" || write.Table != "all" {
		t.Fatalf("governing layers/table lost: %+v", got)
	}
	if !strings.Contains(read.Ask.Origin, WorkspaceTOMLConfigName) || !strings.Contains(read.Ceiling.Origin, "config.toml") {
		t.Fatalf("public writer provenance lost: %+v", got)
	}
	gate, err := ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range gate.Requests {
		if strings.HasPrefix(request.Path, "scopes.") {
			t.Fatal("pure decision metadata activated the shipped request gate")
		}
	}
}

func TestScopedOperationPolicyNeverExceptionsUseShippedFallback(t *testing.T) {
	for _, ceiling := range []string{
		`always=["all"]
on_request=["all.put_objects"]`,
		`always=["all.put_objects"]
never=["all"]`,
	} {
		input, _ := scopedPolicyFixture(t, "packs=[\"aws\"]\n[allow.aws]\n"+ceiling+"\n", "")
		plan, err := ResolveReadableAllowance(input)
		if err != nil {
			t.Fatal(err)
		}
		policy, has := plan.OperationPolicies["aws"]
		if !has {
			t.Fatal("public policy conversion missing")
		}
		got := policy.Decide([]brokerpolicy.Operation{{Source: "aws", Kind: brokerpolicy.Write, Cell: "put_objects", Chain: []brokerpolicy.Resource{{ID: "s3://bkt"}}}}, "", nil)
		if got.Decision != brokerpolicy.Run || got.Operations[0].Ceiling.Value != brokerpolicy.Always {
			t.Fatalf("author's no-never fallback/covering-never exception changed: %+v", got)
		}
	}
}
