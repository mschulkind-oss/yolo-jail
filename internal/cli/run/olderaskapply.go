package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type olderAskRefusal struct{ error }

func (o *Options) olderSourcePrecheck(run *applyRun, source string) func(brokerscope.File) error {
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(run.cfg)).All() {
		if lp.CallBroker != nil && lp.CallBroker.Source == source {
			return func(sf brokerscope.File) error {
				if err := brokerBlindToNever(sf); err != nil {
					return &olderAskRefusal{err}
				}
				if err := o.checkOlderAsksLocked(lp, sf); err != nil {
					return &olderAskRefusal{fmt.Errorf("old policy not replaced: %w; no source write performed; stop this jail with `yolo stop`, then relaunch with `yolo`", err)}
				}
				return nil
			}
		}
	}
	return nil
}

// brokerBlindToNever refuses an apply for a source whose running broker was started by a yolo that
// predates `never` while the user's config holds one: that broker reads only always and on_request,
// so what the apply would leave in place keeps running what the line forbids. The step is a restart.
func brokerBlindToNever(sf brokerscope.File) error {
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || !slices.Contains(config.SourcesNamingNever(user), sf.Source) {
		return nil
	}
	if len(callbrokerinput.BrokersIgnoringNever([]brokerscope.LiveFile{{File: sf}})) == 0 {
		return nil
	}
	return fmt.Errorf("the running %s broker was started by a yolo that predates `never`, so it ignores allow.%s.never; no write performed: stop this jail with `yolo stop`, then relaunch with `yolo`", sf.Source, sf.Source)
}

func privateOlderInput(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("private delivered input unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("private delivered input unreadable")
	}
	return data, nil
}

func sameDeliveredJSON(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

// olderGrantMatchesLaunch reports whether the grant file the delivered input names is the one this
// launch wrote. The grant block's LaunchID is drawn on its own (loopholeGrantBlock), apart from the
// scope file's, so the two never agree and are not compared; the daemon's own live check keys on
// pack, workspace, container and pid (callbroker.currentAuthority).
func olderGrantMatchesLaunch(grant *loopholegrant.Block, input callbrokerinput.Launch, sf brokerscope.File) bool {
	return grant != nil && grant.Pack == input.Pack && grant.Workspace == sf.Workspace && grant.Container == sf.Container &&
		grant.PID == sf.PID && grant.Kinds["read"].Standing == loopholegrant.Standing(input.ReadGrantStanding)
}

// checkOlderAsksLocked reads delivered launch provenance, never the latest approval
// or writable boot record. No consumed-snapshot proof is inferred from any version.
func (o *Options) checkOlderAsksLocked(lp *loopholes.Loophole, sf brokerscope.File) error {
	if sf.PolicySnapshot != nil {
		return errors.New("complete snapshot apply is not active")
	}
	if sf.BrokerStart == "" || sf.BrokerPID <= 0 || sf.CallInput == "" {
		return errors.New("old delivered lease/input is unknown")
	}
	if _, err := brokercore.Live(sf.Source, paths.BrokerScopeFile(sf.Source, sf.LaunchID), sf, sf.BrokerStart); err != nil {
		return err
	}
	data, err := privateOlderInput(sf.CallInput)
	if err != nil {
		return err
	}
	var input callbrokerinput.Launch
	if err := json.Unmarshal(data, &input); err != nil || callbrokerinput.ValidateAuthorityVersion(input, data) != nil || input.Version != "yolo.call-broker-launch/2" || input.Source != sf.Source || input.ScopeFile != paths.BrokerScopeFile(sf.Source, sf.LaunchID) || input.Loophole != lp.Name || input.Pack != lp.Pack || input.DeclarationHash != callbrokerinput.DeclarationHash(lp.CallBroker) {
		return errors.New("old delivered source/declaration/input is unknown or changed")
	}
	old, err := brokerscope.ReadDelivered(sf, input.DeclarationHash, callbrokerinput.InputDigest(data))
	if err != nil {
		return fmt.Errorf("complete old asks unknown: %w", err)
	}
	if old.ReadStanding != input.ReadStanding || old.ReadGrantStanding != input.ReadGrantStanding || old.ReadWithheld != input.ReadWithheld {
		return errors.New("delivered source-read origin disagrees with frozen input")
	}
	_, grant, err := loopholegrant.Read(input.GrantFile)
	if err != nil || !olderGrantMatchesLaunch(grant, input, sf) {
		return errors.New("old source-read grant is unknown or changed")
	}
	// Reconcile every frozen table slot against the complete raw delivered source
	// object, using its OLD ceilings only (not a newly reread user's allowances).
	var raw map[string]json.RawMessage
	if json.Unmarshal(old.Asks.Requests, &raw) != nil {
		return errors.New("complete delivered source requests unknown")
	}
	if err := reconcileFrozenCells(sf.Source, input.Cells, input.EffectiveCells, raw["source"]); err != nil {
		return err
	}
	proposed, exists := o.callBrokerDelivered[lp.Name]
	if !exists {
		return errors.New("complete proposed gate asks unknown")
	}
	if old.Asks.Model != proposed.Model || !sameDeliveredJSON(old.Asks.Requests, proposed.Requests) || !sameDeliveredJSON(old.Asks.Scopes, proposed.Scopes) || !sameDeliveredJSON(old.Asks.PackRead, proposed.PackRead) {
		return errors.New("workspace asks changed without a consuming daemon")
	}
	return nil
}

func (o *Options) blockOlderAskApply(run *applyRun, plan []applyChange) []applyChange {
	if !run.mode.running || run.mode.unknown {
		return plan
	}
	// This is the gate's proposed read, before approval; it supplies no live authority.
	if err := o.captureCallBrokerDelivered(run.cfg, run.current); err != nil {
		o.callBrokerDelivered = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyStepDeadline)
	defer cancel()
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(run.cfg)).All() {
		if lp.CallBroker == nil {
			continue
		}
		files, err := liveScopeFilesSeam(ctx, lp.CallBroker.Source, run.cname, o.Workspace)
		if err == nil && len(files) == 0 {
			live, leaseErr := unclassifiedSourceLifetime(lp.CallBroker.Source)
			if leaseErr != nil {
				err = leaseErr
			} else if live {
				err = errors.New("live source lease has no classified delivered scope/input")
			} else {
				continue
			}
		}
		if err == nil {
			err = brokerlock.WithTransactionContext(ctx, paths.BrokerSourceDir(lp.CallBroker.Source), func() error {
				for _, file := range files {
					sf, err := brokerscope.ReadFile(file.Path)
					if err != nil {
						return err
					}
					if sf.PolicySnapshot != nil {
						continue
					}
					if err := o.checkOlderAsksLocked(lp, sf); err != nil {
						return err
					}
				}
				return nil
			})
		}
		if err == nil {
			continue
		}
		reason := "old policy has not been replaced; complete delivered asks are changed or unknown (" + err.Error() + "); no source authority or ancillary grant writes performed. Stop this jail with `yolo stop`, then relaunch with `yolo`"
		for i := range plan {
			c := &plan[i]
			affected := c.scope.source == lp.CallBroker.Source || c.grant.lp != nil && c.grant.lp.Name == lp.Name || c.key == "requests" && (snapshotRequestLabel(c.label, lp.CallBroker.Source) || snapshotRequestLabel(c.label, lp.Pack))
			if affected {
				c.outcome, c.reason = outcomeOlderAskPending, reason
			}
		}
		plan = append(plan, applyChange{kind: changeKey, sign: "~", label: lp.Name + " old policy", scope: scopeDelta{source: lp.CallBroker.Source}, outcome: outcomeOlderAskPending, reason: reason})
	}
	return plan
}

func unclassifiedSourceLifetime(source string) (bool, error) {
	dir := paths.BrokerSourceDir(source)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".lifetime-") {
			continue
		}
		file, err := os.OpenFile(filepath.Join(dir, entry.Name()), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return false, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// reconcileFrozenCells rebuilds the user and workspace config a frozen cell table could have come
// from, using only the frozen requests and ceilings, and requires the rebuilt decisions to equal
// the frozen ones. A frozen ceiling of off with a refuse decision is a `never` or a narrower false
// (EffectiveCallBrokerCells forces exactly that pair), so it is rebuilt as a `never` on that table
// and cell: the same refusal, never a run, so an unreconcilable difference still fails closed.
func reconcileFrozenCells(source string, cells []loopholedecl.CallBrokerCell, frozen map[string]config.CallBrokerCellPolicy, requestsSource json.RawMessage) error {
	workspace := jsonx.NewOrderedMap()
	requests := jsonx.NewOrderedMap()
	if len(requestsSource) != 0 {
		decoded, err := jsonx.Decode(requestsSource)
		if err != nil {
			return err
		}
		requests.Set(source, decoded)
	}
	workspace.Set("requests", requests)
	user, allows, allow := jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	lists := map[string][]any{"always": {}, "on_request": {}, "never": {}}
	if len(frozen) != len(cells) {
		return errors.New("old delivered cell slots incomplete")
	}
	for _, cell := range cells {
		policy, exists := frozen[cell.Name]
		if !exists || len(policy.Tables) != len(cell.Tables) {
			return errors.New("old delivered table slots incomplete")
		}
		for _, table := range cell.Tables {
			value, exists := policy.Tables[table]
			if !exists {
				return errors.New("old delivered table absent")
			}
			switch {
			case value.Ceiling == "always" || value.Ceiling == "on_request":
				lists[value.Ceiling] = append(lists[value.Ceiling], table+"."+cell.Name)
			case value.Ceiling == "off" && value.Decision == "refuse":
				lists["never"] = append(lists["never"], table+"."+cell.Name)
			case value.Ceiling != "off":
				return errors.New("old delivered ceiling unknown")
			}
		}
	}
	for field, list := range lists {
		if field != "never" || len(list) != 0 {
			allow.Set(field, list)
		}
	}
	allows.Set(source, allow)
	user.Set("allow", allows)
	reconciled, err := config.EffectiveCallBrokerCells(source, &loopholedecl.CallBroker{Source: source, Cells: cells}, user, workspace)
	if err != nil || !reflect.DeepEqual(reconciled, frozen) {
		return errors.New("complete delivered asks do not reconcile frozen cell requests/decisions")
	}
	return nil
}
