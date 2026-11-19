package run

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// ManagedMCPDelivery names an actual composed destination, not a planned copy.
// Refusals are the layer writer's entry names/reasons for this destination.
type ManagedMCPDelivery struct {
	Pack     string
	Agent    string
	Root     string
	Refusals map[string]string
}

// ResolveManagedMCPSignIn reads only the selected originals and the tree the
// layer writer produced. userPacks is the verified pre-staging subset/order.
// sources is independently qualified by the existing one-sign-in source rule.
func ResolveManagedMCPSignIn(servers []mcpauth.OnceServer, packs []*packload.Pack, userPacks []string, deliveries []ManagedMCPDelivery, sources []mcpauth.Source) *mcpsignin.Launch {
	launch := &mcpsignin.Launch{Version: mcpsignin.LaunchVersion}
	agents := []mcpsignin.Agent{}
	for _, delivery := range deliveries {
		var owner *packload.Pack
		for _, pack := range packs {
			if pack != nil && pack.Name == delivery.Pack {
				owner = pack
				break
			}
		}
		if owner == nil || owner.Decl == nil || delivery.Agent == "" {
			continue
		}
		found := false
		for _, agent := range agents {
			if agent.Name == delivery.Agent {
				found = true
			}
		}
		if !found {
			capturable := false
			adapter := packload.MCPAuthAdapterFor(owner)
			for _, source := range sources {
				if source.Pack == owner.Name && source.Validate() == nil && adapter == delivery.Agent && source.AdapterName() == adapter {
					capturable = true
				}
			}
			agentName := delivery.Agent
			script := packload.DeriveScript(owner)
			agents = append(agents, mcpsignin.Agent{Name: agentName, Formats: owner.Decl.MCPSignInFormats, Capturable: capturable, HeaderForm: owner.Decl.MCPHeaderForm, Paste: owner.Decl.MCPSignInPaste, NativeName: func(name, key string) (string, error) { return luahook.MCPNativeName(script, agentName, name, key) }})
		}
		for _, name := range userPacks {
			var pack *packload.Pack
			for _, p := range packs {
				if p != nil && p.Name == name {
					pack = p
					break
				}
			}
			if pack == nil || pack.Decl == nil {
				continue
			}
			originals, err := packload.ReadableSignInBundles(pack)
			if err != nil {
				continue
			}
			layerSources := jailSkillSources(pack, nil)
			for _, original := range originals {
				decl := original.Declaration
				bundle := mcpsignin.Bundle{Pack: pack.Name, Agent: delivery.Agent, Format: decl.Format, Origin: decl.Origin, Revision: decl.Revision, Placement: "not delivered", CallbackRoute: decl.CallbackRoute, Servers: original.Inventory.Servers}
				// A source's audience is a loading boundary too. Root-plugin sources have
				// the declaration's audience; a declaration alone never bypasses it.
				audience := false
				sources, ps := pack.SkillsSources()
				if len(ps) == 0 {
					layerSources = jailSkillSources(pack, sources)
				}
				for _, source := range layerSources {
					if len(source.Agents) == 0 || slices.Contains(source.Agents, delivery.Agent) {
						for root, format := range source.SignInFormats {
							if format == decl.Format && root == filepath.Join(pack.Root, filepath.FromSlash(decl.Bundle)) {
								audience = true
							}
						}
					}
				}
				if !audience {
					continue
				}
				name := packname.Delivered(pack.Namespace, original.Inventory.ManifestName)
				if reason := delivery.Refusals[name]; reason != "" {
					bundle.Reason = reason
				} else {
					reader, e := mcpsignin.OpenDisk(delivery.Root)
					if e != nil {
						bundle.Reason = "delivered tree unavailable"
					} else {
						delivered := mcpsignin.ReadInventory(reader, name, decl.Format)
						manifest, e := reader.ReadFile(filepath.ToSlash(filepath.Join(name, ".claude-plugin/plugin.json")))
						reader.Close()
						var marks map[string]any
						if e == nil {
							e = json.Unmarshal(manifest, &marks)
						}
						if e != nil || marks["x-yolo-managed-by"] != "yolo-jail" || marks["x-yolo-pack"] != pack.Name {
							bundle.Reason = "placement ownership unavailable"
						} else if e = mcpsignin.CheckPreserved(original.Inventory, delivered); e != nil {
							bundle.Reason = "delivered identity differs from retained original"
						} else if len(delivered.Problems) > 0 {
							bundle.Reason = strings.Join(delivered.Problems, "; ")
						} else {
							bundle.Placement = "placed"
							bundle.ManifestName = delivered.ManifestName
							bundle.Servers = delivered.Servers
						}
					}
				}
				launch.Bundles = append(launch.Bundles, bundle)
			}
		}
	}
	once := make([]mcpsignin.Once, 0, len(servers))
	for _, server := range servers {
		once = append(once, mcpsignin.Once{Name: server.Name, URL: server.URL, Transport: server.Transport, ExpectedClientID: server.ExpectedClientID})
	}
	launch.Decisions = mcpsignin.Resolve(once, launch.Bundles, agents)
	return launch
}

func (o *Options) freezeDeliveredMCPSignIn(staging string, packs []*packload.Pack, report *jailcontent.WorkspaceSkillsReport) {
	frozen := o.launchInputMCP
	if !frozen.Proven {
		return
	}
	effective, e := jsonx.Decode([]byte(frozen.EffectiveJSON))
	user, u := jsonx.Decode([]byte(frozen.UserOnlyJSON))
	if e != nil || u != nil {
		return
	}
	em, ok := effective.(*jsonx.OrderedMap)
	um, yes := user.(*jsonx.OrderedMap)
	if !ok || !yes {
		return
	}
	in := LaunchInputContext{EffectiveMCPServers: em, UserOnlyMCPServers: um, MCPScopes: packload.MCPServerScopes(em, um), MCPProven: true, SharedCredentialDir: frozen.SharedCredentialDir, SharedCredentialHome: frozen.SharedCredentialHome}
	servers, err := onceServersOf(in)
	if err != nil {
		return
	}
	_, sources, err := onceShare(in, packs)
	if err != nil {
		return
	}
	var deliveries []ManagedMCPDelivery
	for _, target := range packSkillTargets(packs) {
		refusals := map[string]string{}
		if report != nil {
			for _, notice := range report.PackNotices {
				if slices.Contains(notice.In, "~/"+target.Dest) {
					refusals[notice.Name] = notice.Detail
				}
			}
		}
		owner := ""
		for _, pack := range packs {
			if pack != nil && jailcontent.SkillStagingName(pack.Name) == target.Staging {
				owner = pack.Name
				break
			}
		}
		deliveries = append(deliveries, ManagedMCPDelivery{Pack: owner, Agent: target.Agent, Root: filepath.Join(staging, target.Staging), Refusals: refusals})
	}
	o.launchInputMCP.SignIn = ResolveManagedMCPSignIn(servers, packs, frozen.UserPacks, deliveries, sources)
	o.launchInputMCP.SignIn.Advice = frozen.Advice
	addManagedRecordUsability(o.launchInputMCP.SignIn, servers)
	share := mcpauth.Share{Version: 1, Once: servers, Sources: sources, SignIn: o.launchInputMCP.SignIn}
	for _, row := range mcpauthdaemon.ReadHostStatus(loopholes.StateDirFor(selectedMCPHostService), share) {
		if row["diagnosis"] == "ready" {
			continue
		}
		if step, ok := row["next_step"].(string); ok && step != "" {
			o.pr(o.Stderr).print("MCP sign-in: " + richtext.Escape(step))
		}
	}
}

// HostManagedMCPSignIn uses user-only composition at the unconfined notch.
// Alternate storage and Mac Keychain sessions have no adoptable source.
func HostManagedMCPSignIn(cfg *jsonx.OrderedMap, packs []*packload.Pack, home string, sourceEnabled bool) (*mcpsignin.Launch, error) {
	share, err := HostHeaderShare(cfg, packs, home, home)
	if err != nil {
		return nil, err
	}
	if !sourceEnabled && share.SignIn != nil {
		for i := range share.SignIn.Decisions {
			share.SignIn.Decisions[i].Capturable = false
		}
	}
	return share.SignIn, nil
}

func hostDeliveredMCPSignIn(servers []mcpauth.OnceServer, packs []*packload.Pack, home string, sources []mcpauth.Source) *mcpsignin.Launch {
	names := make([]string, 0, len(packs))
	var deliveries []ManagedMCPDelivery
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil {
			continue
		}
		names = append(names, pack.Name)
		for _, c := range pack.Decl.Contributions() {
			if c.Kind == packdecl.KindSkills && c.Into != "" {
				deliveries = append(deliveries, ManagedMCPDelivery{Pack: pack.Name, Agent: c.Agent, Root: filepath.Join(home, filepath.FromSlash(c.Into))})
			}
		}
	}
	facts := ResolveManagedMCPSignIn(servers, packs, names, deliveries, sources)
	addManagedRecordUsability(facts, servers)
	facts.Callbacks = mcpsignin.PlanCallbacks(facts, "host", false, false, nil)
	return facts
}

// This is a strict descriptor, never NewOnceStore: a launch's read cannot create
// or repair credential state. Unavailable/expired records do not enable a route.
func addManagedRecordUsability(facts *mcpsignin.Launch, servers []mcpauth.OnceServer) {
	if facts == nil || len(servers) == 0 {
		return
	}
	store := &mcpauth.CanonicalStore{Dir: filepath.Join(loopholes.StateDirFor(selectedMCPHostService), "once"), Filename: "records.json", LockName: "records.lock", Wait: 2 * time.Second}
	file, err := store.ReadStatus()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	for i := range facts.Decisions {
		decision := &facts.Decisions[i]
		for _, server := range servers {
			if server.Name != decision.Server {
				continue
			}
			for _, record := range file.Records {
				if record.Server == server.Name && record.Identity == server.Identity && record.State == "live" && record.AccessToken != "" && record.ExpiresAtMS > now {
					decision.UsableRecord = true
				}
			}
		}
	}
}
