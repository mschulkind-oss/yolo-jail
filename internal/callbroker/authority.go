package callbroker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// oncePolicy is only the existing store's encoding of BB-D77's one exact
// command/use/15m, never a pack's timed-resource or Add policy.
var oncePolicy = brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}

// ApprovalAuthority is the standalone host yolo approve adapter. A recorded
// request only locates its original launch; the selected declaration, current
// host ceiling, live scope/grant, classifier and canonical bytes are checked anew.
func ApprovalAuthority(q brokerstore.Request) (brokerstore.Policy, error) {
	if q.LaunchID == "" || !brokercore.ValidLifetimeToken(q.LaunchID) || !brokercore.ValidLifetimeToken(q.Start) || !loopholedecl.ValidBrokerSourceName(q.Set) {
		return brokerstore.Policy{}, errors.New("call broker request has no live host launch; retry the original call")
	}
	path := paths.BrokerScopeFile(q.Set, q.LaunchID)
	sf, err := brokerscope.ReadFile(path)
	if err != nil || sf.Form != brokerscope.FormCallRoot || sf.CallInput == "" || sf.BrokerStart != q.Start || sf.Source != q.Set {
		return brokerstore.Policy{}, errors.New("selected call broker scope is unavailable; relaunch and retry")
	}
	data, err := readPrivateInput(sf.CallInput)
	if err != nil {
		return brokerstore.Policy{}, err
	}
	var in callbrokerinput.Launch
	if json.Unmarshal(data, &in) != nil || in.ScopeFile != path || in.Source != q.Set || in.Front != "cli" || !slices.Equal(in.Roots, sf.Repos) {
		return brokerstore.Policy{}, errors.New("selected call broker input changed; relaunch and retry")
	}
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(q.Set), Source: q.Set}
	h, err := newCLIHandler(sf.CallInput, sf.Workspace, store, brokeraudit.Open(paths.BrokerAuditLog(), nil))
	if err != nil {
		return brokerstore.Policy{}, err
	}
	h.scopePath, h.bound, h.start = path, sf, q.Start
	return h.revalidate(q)
}

// currentAuthority checks all independently revocable host authority without
// acquiring the approval store lock. Workspace Lua/config is never reloaded:
// scope/grant files are the live gate-approved state, with only the current user
// ceiling and selection re-read from host config.
func (h *CLIHandler) currentAuthority() (brokerscope.File, *jsonx.OrderedMap, error) {
	if h.start == "" || h.scopePath == "" || h.input.ScopeFile != h.scopePath || h.bound.CallInput != h.inputFile {
		return brokerscope.File{}, nil, errors.New("call broker has no bound host launch; relaunch and retry")
	}
	if err := h.live(); err != nil {
		return brokerscope.File{}, nil, err
	}
	sf, err := brokercore.Live(h.input.Source, h.scopePath, h.bound, h.start)
	if err != nil || sf.Form != brokerscope.FormCallRoot || sf.CallInput != h.inputFile || sf.CallCLIHash != h.program.sha256 || sf.CallCLIPath != h.program.path || sf.Source != h.input.Source {
		return brokerscope.File{}, nil, errors.New("call broker live scope changed; relaunch and retry")
	}
	for _, root := range sf.Repos {
		if !loopholedecl.ValidCallBrokerRoot(root) {
			return brokerscope.File{}, nil, errors.New("invalid live call-broker root; relaunch and retry")
		}
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		return brokerscope.File{}, nil, errors.New("host user allowance is unreadable; run yolo check and retry")
	}
	packs, complete := config.UserScopeSelectedPacks()
	if !complete {
		return brokerscope.File{}, nil, errors.New("selected pack list is incomplete; run yolo check and retry")
	}
	matched := 0
	for _, pack := range packs {
		modules, refusals, warnings, err := packload.ReadableLoopholeModules(pack)
		if err != nil || len(refusals)+len(warnings) != 0 {
			return brokerscope.File{}, nil, errors.New("selected pack declaration is unreadable; run yolo check and retry")
		}
		for _, module := range modules {
			if module.Decl == nil || module.Decl.CallBroker == nil || module.Decl.CallBroker.Source != h.input.Source {
				continue
			}
			b := module.Decl.CallBroker
			readDeclared := false
			var readGrant loopholedecl.Grant
			for _, grant := range module.Decl.Grants {
				if grant.Kind == "read" {
					readDeclared = true
					readGrant = grant
				}
			}
			if !readDeclared {
				return brokerscope.File{}, nil, errors.New("selected pack has no declared read grant; relaunch and retry")
			}
			requested := jsonx.NewOrderedMap()
			packRequest := jsonx.NewOrderedMap()
			packRequest.Set("read", true)
			requested.Set(pack.Name, packRequest)
			workspaceRequest := jsonx.NewOrderedMap()
			workspaceRequest.Set("requests", requested)
			plan, planErr := config.ResolveLoopholeGrants(config.LoopholeGrantInput{
				User: user, Workspace: workspaceRequest,
				Declarations: []config.LoopholeGrantDeclaration{{Pack: pack.Name, Loophole: module.Name,
					Access: module.Decl.Access, Grants: []loopholedecl.Grant{readGrant}}},
			})
			if planErr != nil || len(plan.Packs) != 1 || len(plan.Packs[0].Kinds) != 1 || plan.Packs[0].Kinds[0].Standing != config.LoopholeKindCandidate {
				return brokerscope.File{}, nil, errors.New("host read ceiling withdrawn; use yolo allow before retrying")
			}
			if module.Name != h.input.Loophole || pack.Name != h.input.Pack || h.input.DeclarationHash == "" || callbrokerinput.DeclarationHash(b) != h.input.DeclarationHash || b.Front != h.input.Front || b.Program != h.input.Program ||
				!slices.Equal(b.Classifier, h.input.Classifier) || b.Executor != h.input.Executor || !reflect.DeepEqual(b.Resources, h.input.Resources) || !reflect.DeepEqual(b.Cells, h.input.Cells) || !reflect.DeepEqual(b.Lookups, h.input.Lookups) {
				return brokerscope.File{}, nil, errors.New("selected pack declaration changed; relaunch and retry")
			}
			matched++
		}
	}
	if matched != 1 {
		return brokerscope.File{}, nil, errors.New("call broker is no longer uniquely selected; select its pack and retry")
	}
	// The exact private grant snapshot this launch published is replaced by
	// config apply. Never scan the state directory for another launch's grant.
	_, grant, err := loopholegrant.Read(h.input.GrantFile)
	if err != nil || grant == nil || grant.PID != sf.PID || grant.Pack != h.input.Pack ||
		grant.Workspace != sf.Workspace || grant.Container != sf.Container ||
		grant.Kinds["read"].Standing != loopholegrant.Granted {
		return brokerscope.File{}, nil, errors.New("read grant withdrawn or unavailable; run yolo request and yolo config apply before retrying")
	}
	if err := h.program.unchanged(); err != nil {
		return brokerscope.File{}, nil, err
	}
	return sf, user, nil
}

// revalidate runs under the store's transaction, including from yolo approve.
// The classifier sees only the recorded hash, never retained stdin or facts.
func (h *CLIHandler) revalidate(q brokerstore.Request) (brokerstore.Policy, error) {
	sf, user, err := h.currentAuthority()
	if err != nil {
		return brokerstore.Policy{}, err
	}
	if q.Set != h.input.Source || q.Repo != h.input.Source || q.Start != h.start || q.LaunchID != sf.LaunchID || q.Workspace != sf.Workspace || q.Jail != h.input.FrontJailID || len(q.CallWrites) == 0 || len(q.Argv) == 0 || len(q.OriginalArgv) == 0 || strings.ContainsAny(q.StdinDigest, "\r\n") {
		return brokerstore.Policy{}, errors.New("request does not match the live host launch; retry the original call")
	}
	classifier := h.classifier
	classifier.Roots = slices.Clone(sf.Repos)
	answer, err := classifier.ClassifyCLIDigest(context.Background(), q.OriginalArgv, q.StdinDigest, h.lookupWithClassifier(q.Jail, classifier))
	if err != nil || answer.Classification == nil || len(answer.Classification.Writes) == 0 {
		return brokerstore.Policy{}, errors.New("current classifier refused this call; retry the original call")
	}
	expected := callRequest(h.input.Source, q.Workspace, q.Jail, q.Start, q.LaunchID, *answer.Classification, nil)
	expected.StdinDigest = q.StdinDigest
	expected.OriginalArgv = slices.Clone(q.OriginalArgv)
	expected.Digest = brokerstore.CommandDigest(expected)
	if expected.Digest != q.Digest || !reflect.DeepEqual(expected.Argv, q.Argv) || !reflect.DeepEqual(expected.OriginalArgv, q.OriginalArgv) || !reflect.DeepEqual(expected.CallWrites, q.CallWrites) {
		return brokerstore.Policy{}, errors.New("current canonical command, ordered writes or resources changed; retry the original call")
	}
	for _, w := range answer.Classification.Writes {
		if len(w.Chain) == 0 || !slices.Contains(sf.Repos, w.Chain[len(w.Chain)-1].ID) {
			return brokerstore.Policy{}, errors.New("write left the approved root; retry after yolo config apply")
		}
		p := h.input.EffectiveCells[w.Cell]
		frozen, exists := p.Tables["all"]
		current := config.CallBrokerCurrentCeiling(user, h.input.Source, "all", w.Cell)
		if !exists || frozen.Decision == "refuse" || frozen.Request == "false" || frozen.Ceiling == "off" ||
			current == "off" || frozen.Ceiling == "always" && current != "always" {
			return brokerstore.Policy{}, errors.New("one write's host ceiling or approved request was withdrawn; use yolo allow and yolo config apply before retrying")
		}
	}
	return oncePolicy, nil
}
