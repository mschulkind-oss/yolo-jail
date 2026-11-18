package config

import (
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const scopedBrokerPolicyModel = "scoped/1"

func policyModelRecordKey(source string) string { return source + ".policy-model" }

func selectedDeclarationRecordKey(source ScopeSource) string {
	if source.NativeDeclaration {
		return source.Source + ".brokered-declaration"
	}
	return callBrokerScopeRecordKey(source.Source)
}

// PolicyModelApproved reports approval metadata only, never runtime authority or consumption.
func PolicyModelApproved(workspace, source string) bool {
	record, _, err := readScopeRecord(ApprovalScopePath(workspace))
	return err == nil && recordHasPolicyModel(record, source)
}

func recordHasPolicyModel(record scopeRecord, source string) bool {
	return slices.Equal(record[policyModelRecordKey(source)], []string{scopedBrokerPolicyModel})
}

func sourceHasWrites(source ScopeSource) bool {
	return len(source.policyWriteLines) > 0 || slices.ContainsFunc(source.Grants, func(grant brokerscope.Grant) bool { return grant.Kind != brokerscope.KindRead })
}

func nativeDeclarationScope(source *ScopeSource, broker ScopeBroker) {
	if broker.Brokered.Source != broker.Source {
		return
	}
	source.NativeDeclaration = true
	source.DeclarationRequiresApproval = broker.Brokered.ReadMaxFor != ""
	source.Disclosure = []string{"native broker: " + broker.Source,
		"declaration_sha256=" + loopholedecl.BrokeredDeclarationHash(&broker.Brokered)}
	if broker.Brokered.ReadMaxFor != "" {
		source.Disclosure = append(source.Disclosure, "read_max_for="+broker.Brokered.ReadMaxFor)
	}
}

func selectedDeclarationChanged(source ScopeSource, previous []string) bool {
	if source.NativeDeclaration && len(previous) == 0 && !source.DeclarationRequiresApproval {
		return false
	}
	// A legacy call declaration already disclosed its entire body. Adding the
	// canonical hash does not itself re-prompt or silently rewrite that approval.
	current := source.Disclosure
	if len(previous) > 0 && !slices.ContainsFunc(previous, func(line string) bool { return strings.HasPrefix(line, "declaration_sha256=") }) {
		current = slices.DeleteFunc(slices.Clone(current), func(line string) bool { return strings.HasPrefix(line, "declaration_sha256=") })
	}
	current = slices.Clone(current)
	previous = slices.Clone(previous)
	slices.Sort(current)
	slices.Sort(previous)
	return len(current) > 0 && !sameStrings(previous, current)
}

// policyApprovalCheck exposes the legacy-held projection only when the gate is
// already asking, or a caller explicitly records an approval. It is not runtime
// consumption or permission to bypass a native broker's current read floor.
func policyApprovalCheck(check *ScopeCheck) *ScopeCheck {
	if check == nil {
		return nil
	}
	approved := *check
	approved.Sources = slices.Clone(check.Sources)
	for i, source := range approved.Sources {
		if source.policyPending != nil {
			approved.Sources[i] = *source.policyPending
			approved.Sources[i].FirstRead = true
		}
	}
	return &approved
}

func (out *scopeOutcome) publishPolicyApproval(check *ScopeCheck) {
	for _, source := range check.Sources {
		if source.Dormant || !source.concrete() || len(source.Disclosure) == 0 {
			continue
		}
		out.next[policyModelRecordKey(source.Source)] = []string{scopedBrokerPolicyModel}
		if len(source.Disclosure) > 0 {
			out.next[selectedDeclarationRecordKey(source)] = slices.Clone(source.Disclosure)
		}
	}
}
