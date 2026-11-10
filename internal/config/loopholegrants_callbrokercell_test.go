package config

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// A shipped call-broker pack whose source is named like the pack (circleci, aws-s3) keeps its
// grant kinds and its cell ceilings in one table: `yolo allow <source> on_request all.<cell>`
// writes `all.<cell>` beside the pack's `read`. The grant reader must leave the cell entries to
// the call-broker reader instead of refusing the whole config, or no write cell of such a pack
// can ever be allowed.
func TestLoopholeGrantsLeaveSameNamedCallBrokerCellCeilingsToTheCallBroker(t *testing.T) {
	user := jsonx.NewOrderedMap()
	allow := jsonx.NewOrderedMap()
	table := jsonx.NewOrderedMap()
	table.Set("always", []any{"read"})
	table.Set("on_request", []any{"all.put_objects", "working_set.put_objects", "all.delete_objects"})
	allow.Set("aws-s3", table)
	user.Set("allow", allow)
	decl := LoopholeGrantDeclaration{Pack: "aws-s3", Loophole: "aws-s3-broker",
		Grants: []loopholedecl.Grant{{Kind: "read", Text: "read objects"}}}
	plan, err := ResolveLoopholeGrants(LoopholeGrantInput{User: user, Workspace: jsonx.NewOrderedMap(),
		Declarations: []LoopholeGrantDeclaration{decl}})
	if err != nil {
		t.Fatalf("cell ceilings beside the read grant refused the config: %v", err)
	}
	if len(plan.Packs) != 1 || len(plan.Packs[0].Kinds) != 1 || plan.Packs[0].Kinds[0].Ceiling != LoopholeCeilingAlways {
		t.Fatalf("read grant standing = %+v", plan.Packs)
	}

	// A kind that is neither declared nor a table.cell token is still refused.
	table.Set("on_request", []any{"writes"})
	if _, err := ResolveLoopholeGrants(LoopholeGrantInput{User: user, Workspace: jsonx.NewOrderedMap(),
		Declarations: []LoopholeGrantDeclaration{decl}}); err == nil {
		t.Fatal("an undeclared grant kind was accepted")
	}
}
