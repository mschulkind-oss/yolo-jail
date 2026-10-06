package run

// bedrockmodels.go is the launch's half of the FETCHED LIST (docs/design/model-lists-and-pickers.md
// OQ-MM6, ruled 2026-10-05): where no selected pack and no user config gives a platform's provider a
// model list, the launch asks the platform's credential service for the region's list and hands it
// to the jail through the composed provider table, under its own key (packload.FetchedModelsKey), so
// an agent with a catalog of its own keeps it (MM-D32) and only the wire bridge and an agent with
// nothing else to start on read it.
//
// # Where in the launch, and why there
//
// In the channel composition, after the credential gate has composed each agent's delivery once:
// the gate is what knows the region an agent will be handed (its region fill reads ~/.aws/config
// for the credential's profile), and the list must be in the table before the env derives run,
// since copilot's start model comes from one. So the fetch reads the gate's first answer, writes
// the list onto the table, and the gate composes again. A launch that wants no list composes once.
//
// # How the list is obtained, cheapest first
//
//  1. THE SERVICE'S OWN ANSWER (awsauthdaemon's modellists.go), asked on its private host socket:
//     its cache when a day old or less, else a fetch within ModelListBudget, else the cache's last
//     good list. A warm cache costs one local round trip.
//  2. THE SAME FETCH, RUN BY THE LAUNCH, when no service is running to ask: the first Bedrock
//     launch on a machine, which composes before it starts the service. It runs the service's own
//     code with the configured profile, writes the same cache under the same lock, and is bounded
//     by the same budget. Without it the first launch would have no list, and one that needs a list
//     would be refused before it started the service that could fetch it, every time.
//
// # When it refuses
//
// An agent whose pack declares `needs_model_list` for the platform, whose profile names no `model`,
// on a provider no pack or config gives a list, with no list obtained either way: the launch stops,
// saying why and what to add (the maintainer's ruling: "if there's no model list, the agent has no
// default handling, and we can't fetch models, we just fail and tell them that"). An agent that
// wants a list only for the bridge is never refused: the launch says what the bridge then does
// with no maker to read, and how to give it a list.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// ModelListRequest is one fetched-list question the launch asks (Options.FetchModelList).
type ModelListRequest struct {
	// Service is the loophole serving the platform's credentials (packload.ListWant.Service).
	Service string
	// Enabled says whether this launch's config enables that loophole.
	Enabled bool
	// Profile is the loophole's configured `profile` setting, which the service fetches as.
	Profile string
	Region  string
}

// DefaultFetchModelList is the production FetchModelList, installed by the front door: the
// service's answer on its host socket, else the same fetch run here (the file comment's order).
func DefaultFetchModelList(req ModelListRequest) awsauthdaemon.ModelListAnswer {
	if req.Service != awsauthdaemon.LoopholeName {
		return awsauthdaemon.ModelListAnswer{Note: fmt.Sprintf("yolo fetches no model list through %q", req.Service)}
	}
	if !req.Enabled {
		return awsauthdaemon.ModelListAnswer{Note: "the " + req.Service + " service, which fetches it, is not " +
			"enabled (loopholes." + req.Service + ".enabled)"}
	}
	return fetchModelListAt(awsauthdaemon.HostSocketPath(paths.HostSingletonSocket(req.Service)),
		filepath.Join(loopholes.StateDirFor(req.Service), awsauth.ModelCacheFileName), awsauth.ExecRunner(), req)
}

// fetchModelListAt is DefaultFetchModelList over the service's host socket, its cache file and
// the `aws` runner the launch's own fetch would use, so a test can stand in for all three.
func fetchModelListAt(socket, cachePath string, runner awsauth.Runner, req ModelListRequest) awsauthdaemon.ModelListAnswer {
	if ans, err := askModelListService(socket, req.Region, awsauthdaemon.ModelListBudget); err == nil {
		return ans
	}
	ans := awsauthdaemon.ObtainModelList(awsauthdaemon.ModelListSource{
		CachePath: cachePath,
		Profile:   req.Profile,
		Lister:    awsauth.ModelLister{Run: runner},
	}, req.Region, awsauthdaemon.ModelListBudget)
	if ans.Source == "fetched" {
		ans.Source = "fetched by this launch, the " + req.Service + " service not running yet"
	}
	return ans
}

// modelListMargin is how long past the budget the launch keeps reading the service's answer.
const modelListMargin = time.Second

// askModelListService asks the service on its private host socket, a plain AF_UNIX socket the
// daemon serves with no front (awsauthdaemon.HostSocketPath).
func askModelListService(socket, region string, budget time.Duration) (awsauthdaemon.ModelListAnswer, error) {
	conn, err := net.DialTimeout("unix", socket, modelListMargin)
	if err != nil {
		return awsauthdaemon.ModelListAnswer{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(budget + modelListMargin))
	body, err := json.Marshal(map[string]any{"action": awsauthdaemon.ModelListAction,
		awsauthdaemon.ModelListRegionKey: region, "budget_ms": budget.Milliseconds()})
	if err != nil {
		return awsauthdaemon.ModelListAnswer{}, err
	}
	if err := frameproto.WriteRequest(conn, body); err != nil {
		return awsauthdaemon.ModelListAnswer{}, err
	}
	var stdout, stderr bytes.Buffer
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			return awsauthdaemon.ModelListAnswer{}, err
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				return awsauthdaemon.ModelListAnswer{}, err
			}
			if rc != 0 {
				// An older service that does not know the action says so here, and the launch
				// then fetches the list itself.
				return awsauthdaemon.ModelListAnswer{}, errors.New(firstNonEmptyLine(stderr.String(), "no diagnostic"))
			}
			var ans awsauthdaemon.ModelListAnswer
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ans); err != nil {
				return awsauthdaemon.ModelListAnswer{}, err
			}
			return ans, nil
		}
	}
}

// composeFetchedLists obtains a list for every provider of this launch that wants one and writes
// each onto providers. changed says the table changed, so the gate must compose again. The error
// is the refusal the file comment describes, one line per agent left with nothing to start on.
func (o *Options) composeFetchedLists(cfg *jsonx.OrderedMap, packs []*packload.Pack, providers *jsonx.OrderedMap,
	resolved map[string]packload.ResolvedProfile, scope *packload.CredentialScope) (changed bool, err error) {
	if o.FetchModelList == nil {
		return false, nil
	}
	wants := packload.ListWants(packs, providers, resolved, scope)
	if len(wants) == 0 {
		return false, nil
	}
	enabled := map[string]bool{}
	for _, lp := range loopholes.NewHostSet(cfgMap(cfg, "loopholes")).Enabled() {
		enabled[lp.Name] = true
	}
	setting := packload.LoopholeSettingIn(cfg)
	// A service whose pointer this nested launch inherits is not asked (SSO-D5): its answer is a
	// failed fetch, so the launch says and refuses exactly what a real failure makes it.
	inherited := o.inheritedLoopholes(o.runtime, cfg)
	var problems []string
	for _, w := range wants {
		if w.Region == "" {
			// No region reaches its agents: the region pre-flight refuses that launch, naming
			// what it asked, which is the problem to fix first.
			continue
		}
		ans := awsauthdaemon.ModelListAnswer{Note: "no selected pack names a service that serves " +
			w.Platform + " credentials"}
		if _, ok := inherited[w.Service]; ok && w.Service != "" {
			ans = inheritedModelListAnswer(w.Service)
		} else if w.Service != "" {
			ans = o.FetchModelList(ModelListRequest{Service: w.Service, Enabled: enabled[w.Service],
				Profile: setting(w.Service, "profile"), Region: w.Region})
		}
		if ans.Has() {
			list := make([]packload.FetchedModel, 0, len(ans.List.Models))
			for _, m := range ans.List.Models {
				list = append(list, packload.FetchedModel{ID: m.ID, Vendor: m.Vendor, Name: m.Name,
					Legacy: m.Legacy, Created: m.Created})
			}
			packload.SetFetchedModels(providers, w.Provider, list)
			changed = true
			o.noteFetchedList(w, ans)
			continue
		}
		if len(w.Needs) == 0 {
			o.noteFetchedList(w, ans)
			continue
		}
		agents := make([]string, 0, len(w.Needs))
		for agent := range w.Needs {
			agents = append(agents, agent)
		}
		sort.Strings(agents)
		for _, agent := range agents {
			problems = append(problems, fetchedListRefusal(w, agent, w.Needs[agent], ans.Note))
		}
	}
	if len(problems) > 0 {
		return changed, errors.New(strings.Join(problems, "\n"))
	}
	return changed, nil
}

// fetchedListRefusal is the one refusal line for agent, left with nothing to start on.
func fetchedListRefusal(w packload.ListWant, agent, profile, why string) string {
	why = strings.TrimPrefix(why, "the fetch failed: ")
	if why == "" {
		why = "the service answered no model for it"
	}
	return fmt.Sprintf("%s has no model to start on for profile %q: provider %q (%s, region %s) has no model "+
		"list from a pack or your config, %s's pack says it has no %s catalog of its own, and yolo could not "+
		"fetch the region's list: %s. Fix that and launch again; or name a model with \"model\" on profile %q, "+
		"or give the provider a list with a pack's `models` contribution or \"providers.%s.models\" in "+
		"~/.config/yolo-jail/config.jsonc",
		agent, profile, w.Provider, w.Platform, w.Region, agent, w.Platform, strings.TrimRight(why, "."),
		profile, w.Provider)
}

// noteFetchedList says, once per launch, where provider's list came from, or why there is none.
func (o *Options) noteFetchedList(w packload.ListWant, ans awsauthdaemon.ModelListAnswer) {
	var line string
	switch {
	case ans.Has() && ans.Source == "stale cache":
		line = fmt.Sprintf("[yellow]Model list for provider %q (%s, %s): %d models from the cache, fetched %s ago and "+
			"not refreshed: %s[/yellow]", w.Provider, w.Platform, w.Region, len(ans.List.Models),
			ageOf(o, ans.List), richtext.Escape(strings.TrimRight(ans.Note, ".")))
	case ans.Has():
		line = fmt.Sprintf("[dim]Model list for provider %q (%s, %s): %d models, %s, for %s.[/dim]",
			w.Provider, w.Platform, w.Region, len(ans.List.Models), sourcePhrase(o, ans), strings.Join(w.Agents, ", "))
	default:
		// WHAT THE BRIDGE DOES WITH NO MAKERS, per route, since core cannot tell which route each
		// agent's derive chose: the Messages route translates every model it does not know as
		// Anthropic's, and Bedrock's own invoke route passes every such model through as sent
		// (wirebridged's messages.go and invoke.go).
		line = fmt.Sprintf("[yellow]No model list for provider %q (%s, %s): %s. The wire bridge then knows no "+
			"model's maker for %s: a model sent to its Messages route is translated, a Claude model's too, and "+
			"one sent to Bedrock's own invoke route is passed through as sent, which only a Claude model takes. "+
			"Fix that and launch again, or give the provider a list with a pack's `models` contribution or "+
			"\"providers.%s.models\" in ~/.config/yolo-jail/config.jsonc.[/yellow]",
			w.Provider, w.Platform, w.Region, richtext.Escape(strings.TrimRight(ans.Note, ".")),
			strings.Join(w.Agents, ", "), w.Provider)
	}
	if o.fetchedListNotes == nil {
		o.fetchedListNotes = map[string]bool{}
	}
	if o.fetchedListNotes[line] {
		return
	}
	o.fetchedListNotes[line] = true
	o.pr(o.Stderr).print(line)
}

// sourcePhrase names where a fresh list came from.
func sourcePhrase(o *Options, ans awsauthdaemon.ModelListAnswer) string {
	switch ans.Source {
	case "cache":
		return "from the cache, fetched " + ageOf(o, ans.List) + " ago"
	case "":
		return "fetched"
	}
	return ans.Source
}

// ageOf is how long ago list was fetched, rounded for a person.
func ageOf(o *Options, list awsauth.ModelList) string {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	age := now().Sub(list.FetchedAt())
	switch {
	case age < time.Minute:
		return "moments"
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age/time.Minute))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age/time.Hour))
	}
	return fmt.Sprintf("%d days", int(age/(24*time.Hour)))
}
