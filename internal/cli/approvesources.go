package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// approvalStores joins core-owned authorities, sources declared by the user-scope selected
// packs, and safe on-disk stores whose source is no longer known to be selected. Discovery is a
// user-scope prediction, not proof of a request's launch selection; only core's existing git and
// GitHub adapters can grant, and pack sources stay deny-only until a core adapter exists.
func approvalStores() ([]*brokerstore.Store, []string, error) {
	selected := map[string]*brokerstore.Store{}
	owners := map[string]string{}
	var diagnostics []string
	packs, complete := config.UserScopeSelectedPacks()
	defer packload.ReleaseEmbedded()
	if !complete {
		diagnostics = append(diagnostics, "INCOMPLETE: the user-scope pack selection could not be fully resolved; run `yolo check`, repair or fetch the named packs, then retry. Sources with unknown selection are listed but cannot grant approvals.")
	}
	for _, pack := range packs {
		modules, refusals, warnings, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("INCOMPLETE: cannot read loophole declarations for selected pack %s: %v; run `yolo check`, repair the pack, then retry.", pack.Name, err))
			continue
		}
		if len(refusals) != 0 || len(warnings) != 0 {
			details := append(append([]string(nil), refusals...), warnings...)
			diagnostics = append(diagnostics, fmt.Sprintf("INCOMPLETE: selected pack %s has unreadable loophole declarations: %s; run `yolo check`, repair the pack, then retry.", pack.Name, strings.Join(details, "; ")))
		}
		for _, module := range modules {
			if module.Decl == nil || module.Decl.Brokered == nil {
				continue
			}
			source := module.Decl.Brokered.Source
			if owner, exists := owners[source]; exists {
				if len(diagnostics) == 0 || !strings.HasPrefix(diagnostics[len(diagnostics)-1], "INCOMPLETE:") {
					diagnostics = append(diagnostics, fmt.Sprintf("INCOMPLETE: source %s is declared by both selected packs %s and %s; repair the duplicate ownership, then retry.", source, owner, pack.Name))
				}
				continue
			}
			owners[source] = pack.Name
			// Native adapters are authority, not just store format. Require both the source's
			// matching shipped pack identity and loader-established origin; a configured pack
			// can freely use either source label, or even shadow the shipped pack's name.
			if pack.Official && pack.Name == source && source == gitbroker.Source {
				selected[source] = gitbroker.ApprovalStore()
			} else if pack.Official && pack.Name == source && source == ghbroker.Source {
				selected[source] = ghbroker.ApprovalStore()
			} else {
				selected[source] = genericApprovalStore(source)
			}
		}
	}

	// Direct child source dirs are only store identifiers. Token validation precedes path
	// construction; symlinks and non-directories are ignored, and no contents are executed.
	entries, err := os.ReadDir(paths.BrokerDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, diagnostics, fmt.Errorf("read approval source directories under %s: %w", paths.BrokerDir(), err)
	}
	bySource := make(map[string]*brokerstore.Store, len(selected)+len(entries))
	for source, store := range selected {
		status := "selected"
		if !complete || len(diagnostics) != 0 {
			status = "unknown"
		}
		store.ApprovalStatus = status
		if status == "unknown" {
			store.Revalidate, store.Entries = nil, nil
			store.ApprovalDiagnostic = strings.Join(diagnostics, " ")
		}
		bySource[source] = store
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !loopholedecl.ValidBrokerSourceName(name) {
			continue
		}
		if _, registered := bySource[name]; registered {
			continue
		}
		store := genericApprovalStore(name)
		store.ApprovalStatus = "removed"
		if !complete || len(diagnostics) != 0 {
			store.ApprovalStatus = "unknown"
			store.ApprovalDiagnostic = strings.Join(diagnostics, " ")
		}
		bySource[name] = store
	}

	names := make([]string, 0, len(bySource))
	for name := range bySource {
		names = append(names, name)
	}
	sort.Strings(names)
	stores := make([]*brokerstore.Store, 0, len(names))
	for _, name := range names {
		stores = append(stores, bySource[name])
	}
	return stores, diagnostics, nil
}

// genericApprovalStore publishes request outcomes to yolo wait/history and audits decisions
// through the existing source-neutral core event path. Its nil Revalidate intentionally denies
// all grants until core installs an authority adapter.
func genericApprovalStore(source string) *brokerstore.Store {
	return &brokerstore.Store{Dir: paths.BrokerSourceDir(source), Source: source, PublishRecords: true,
		Audit: brokercore.StoreAudit(brokeraudit.Open(paths.BrokerAuditLog(), nil), source)}
}
