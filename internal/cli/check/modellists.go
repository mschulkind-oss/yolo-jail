package check

// modellists.go reports what the `models` contribution kind could not do as written
// (docs/design/model-lists-and-pickers.md §7.2: "yolo check names the duplicate", and names an
// `only` id nothing added). The launch applies the kind silently — a duplicate keeps its first
// writer, an `only` id nobody added is dropped — because none of those refuses a launch; this
// is where an author or a user hears about them.
//
// It is also where the lists' CURRENCY is checked (§9, MM-D16): a model id a composed list names
// that no installed agent's own catalog knows is a warning, and when no catalog could be read the
// check says it could not ask. modelCatalogReport is that half.
//
// NOT A SECOND COMPOSITION: it calls packload.ComposeProviders, the composition the launch
// runs, and reads its notes (packload.WithModelNotes), so the report and the table cannot
// disagree about what was applied.

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/modelcatalog"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// modelListNotes returns one warning per note the selected packs' `models` contributions
// produce over the merged config's `providers`, nil when there is none. A table that does not
// compose says nothing here: the pairing gate beside it already reports that.
func modelListNotes(packs []*packload.Pack, merged *jsonx.OrderedMap) []string {
	var notes []string
	if _, err := packload.ComposeProviders(subMap(merged, "providers"), packs,
		packload.WithModelNotes(func(n string) { notes = append(notes, "Model list: "+n) })); err != nil {
		return nil
	}
	return notes
}

// unnarrowedMenuReport is MM-D5's line (docs/design/model-lists-and-pickers.md: "opencode cannot
// shape its menu without refusing, so with the switch off it gets no whitelist, and `yolo check`
// says its menu is then not narrowed"; MM-D29 the mechanism): one WARNING per agent and provider
// whose model menu yolo does not narrow to the provider's list because the governing profile turns
// `enforce_models` off. Which agents, lists and profiles count is packload.UnnarrowedMenus', over
// the composition and the profile resolution the launch runs; the agents are the programs whose
// pack declares `exact_menu_refuses`, so core names none.
//
// A WARNING and never a refusal: the launch starts, the menu shows more than the list, and the
// agent runs a model off it, which is what the switch asked for everywhere but this menu. Inputs
// that do not compose or resolve say nothing here: the protocol-pairing prediction beside it
// reports that. Like every prediction in this section it reads the configured `profile`, never a
// `-p`.
func unnarrowedMenuReport(r *reporter, packs []*packload.Pack, merged *jsonx.OrderedMap,
	userProfiles func() (map[string]packload.UserProfile, error)) {
	profiles := config.ConfigProfileTable(merged, packs)
	if len(profiles) == 0 {
		return
	}
	providers, err := packload.ComposeProviders(subMap(merged, "providers"), packs)
	if err != nil || providers == nil {
		return
	}
	declared, err := userProfiles()
	if err != nil {
		return
	}
	resolved, err := packload.ResolveProfiles(packs, declared, providers)
	if err != nil {
		return
	}
	sets := packload.ProfileSets(config.ConfigProfileSets(merged, packs))
	for _, m := range packload.UnnarrowedMenus(packs, providers, resolved, profiles, sets) {
		r.warn(fmt.Sprintf("Model list: yolo does not narrow %s's menu for provider %q to its list, "+
			"because profile %q turns enforce_models off", m.Agent, m.Provider, m.Profile),
			fmt.Sprintf("pack %s says %s's menu can show exactly a list only through a filter that "+
				"also refuses every model off it, so yolo writes that filter only while the profile's "+
				"enforce_models is on. With it off, %s's menu for %q is not held to the list: it can "+
				"offer the models of %s's own catalog for that provider, and %s runs a model off the "+
				"list when one is picked. To narrow the menu, drop \"enforce_models\": false from "+
				"profile %q, which turns the list's refusals back on too",
				m.Pack, m.Agent, m.Agent, m.Provider, m.Agent, m.Agent, m.Profile))
	}
}

// modelCatalogReport is MM-D16's currency check (docs/design/model-lists-and-pickers.md §9), over
// the provider table the launch composes from the selected packs and the merged config:
//
//   - every model id a composed provider lists is looked up in the catalogs of the selected
//     packs' installed agents, the files each pack declares (`model_catalog`, MM-D19);
//   - an id no catalog read knows is a WARNING, never a refusal, one line per provider;
//   - when no catalog could be read, one SKIP says the check could not ask, and why for each
//     agent that declares one: "not found" and "could not ask" are different answers;
//   - when every id is known, one pass names the catalogs that were read.
//
// A provider every declared endpoint of which is a local address is left out (MM-D20): its ids
// are what the user's own server serves, which no vendor catalog lists. An id counts as known
// when any catalog read names it, under any of its providers. It reads files only: no agent is
// started and no network is reached, so an agent that is not installed is one it could not ask.
func (o *Options) modelCatalogReport(r *reporter, packs []*packload.Pack, merged *jsonx.OrderedMap) {
	providers, err := packload.ComposeProviders(subMap(merged, "providers"), packs)
	if err != nil || providers == nil {
		return
	}
	listed, total := listedModelIDs(providers)
	if total == 0 {
		return
	}
	known := map[string]bool{}
	var read, unread []string
	for _, c := range o.declaredCatalogs(packs) {
		switch {
		case c.dir == "":
			unread = append(unread, c.name+": "+c.missing)
		case !modelcatalog.Installed(c.dir):
			unread = append(unread, c.name+": "+c.missing)
		default:
			cat := modelcatalog.Read(c.dir, c.globs)
			if cat.Files == 0 {
				unread = append(unread, fmt.Sprintf("%s: installed at %s, but no file its pack names (%s) "+
					"reads as a catalog, so this release keeps it somewhere else", c.name, c.dir,
					strings.Join(c.globs, ", ")))
				continue
			}
			for id := range cat.IDs {
				known[id] = true
			}
			label := c.name
			if v := modelcatalog.Version(c.dir); v != "" {
				label += " " + v
			}
			read = append(read, label)
		}
	}
	if len(read) == 0 {
		note := "no selected pack declares a catalog yolo can read: an agent that keeps its catalog " +
			"inside its program declares none"
		if len(unread) > 0 {
			note = strings.Join(unread, "; ")
		}
		r.skip(fmt.Sprintf("Model list: %s not checked against an agent's own catalog — no "+
			"installed agent's catalog could be read", plural(total, "listed model id")), note)
		return
	}
	checked := "checked against " + strings.Join(read, ", ") + "'s catalog"
	if len(unread) > 0 {
		checked += "; not read: " + strings.Join(unread, "; ")
	}
	names := make([]string, 0, len(listed))
	for name := range listed {
		names = append(names, name)
	}
	sort.Strings(names)
	warned := false
	for _, name := range names {
		var missing []string
		for _, id := range listed[name] {
			if !known[id] {
				missing = append(missing, fmt.Sprintf("%q", id))
			}
		}
		if len(missing) == 0 {
			continue
		}
		warned = true
		r.warn(fmt.Sprintf("Model list: provider %q lists %s, which no installed agent's catalog knows",
			name, strings.Join(missing, ", ")),
			checked+". A warning, never a refusal: the id may be newer than that catalog, or "+
				"retired, and an agent that starts on a retired id fails at its first request. "+
				"Name a current id in your `providers."+name+".models`")
	}
	if !warned {
		r.ok(fmt.Sprintf("Model list: every listed model id is in an installed agent's catalog (%s)",
			strings.Join(read, ", ")))
	}
}

// declaredCatalog is one selected program's declared model catalog, and where this check looks
// for the installed package: dir, "" with missing saying why when there is nowhere to look.
type declaredCatalog struct {
	name    string
	globs   []string
	dir     string
	missing string
}

// declaredCatalogs lists the model catalogs the selected packs' npm programs declare, each with
// the package directory this notch installed it into: in a jail the jail's npm prefix
// (<prefix>/lib/node_modules/<package>, where its launcher installs it), at the host the host
// floor's copy, which is what `yolo host -- <agent>` runs (host-tool-provisioning.md).
//
// THE JAIL'S PREFIX IS THE ENTRYPOINT'S RULE (entrypoint.NewEnv: $NPM_CONFIG_PREFIX, else
// $HOME/.npm-global), the one every generated launcher installs by, never a second reading of it:
// the macos-user sandbox's closed environment list (macosuser.sandboxEnvPairs) names no prefix,
// leaving the default to those launchers and rc files, so a process there can carry HOME alone.
//
// THE HOST'S COPY IS THE ONE THE FLOOR'S DISPOSITION SAYS `yolo host` RUNS (hostfloor.Status, the
// answer the Host agent floor section prints), never whatever record the prefix still holds: a
// program with no floor entry (`host_floor` leaves its pack out, or this machine cannot hold it)
// runs from the launch's PATH (OQ-HE11), so a record left from before is a copy no launch runs,
// and a program not yet provisioned is one the check could not ask.
func (o *Options) declaredCatalogs(packs []*packload.Pack) []declaredCatalog {
	inJail := o.getenv("YOLO_VERSION") != ""
	var in []hostfloor.PackPrograms
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		in = append(in, hostfloor.PackPrograms{Pack: p.Name, Installs: installs})
	}
	var floor *hostfloor.Floor
	progs := map[string]hostfloor.Program{}
	if !inJail {
		list := hostfloor.Programs(in)
		for _, p := range list {
			progs[p.Bin()] = p
		}
		floor = o.hostFloor(list)
	}
	var out []declaredCatalog
	for _, p := range in {
		for _, inst := range p.Installs {
			if inst.Kind != "npm" || len(inst.ModelCatalog) == 0 {
				continue
			}
			c := declaredCatalog{name: inst.Bin, globs: inst.ModelCatalog}
			name, _ := packdecl.SplitNpmSpec(inst.Package)
			if inJail {
				vars := map[string]string{"HOME": o.getenv("HOME"), "JAIL_HOME": o.getenv("JAIL_HOME")}
				if prefix := o.getenv("NPM_CONFIG_PREFIX"); prefix != "" {
					vars["NPM_CONFIG_PREFIX"] = prefix
				}
				// Neither a prefix nor a home: NewEnv would guess the container's /home/agent, a
				// directory this process was not told about, so there is nowhere to look.
				if vars["NPM_CONFIG_PREFIX"] == "" && vars["HOME"] == "" && vars["JAIL_HOME"] == "" {
					c.missing = "this jail names neither an npm prefix (NPM_CONFIG_PREFIX) nor a HOME, " +
						"so there is nowhere to look"
					out = append(out, c)
					continue
				}
				c.dir = filepath.Join(entrypoint.NewEnv(vars).NpmPrefix, "lib", "node_modules",
					filepath.FromSlash(name))
				c.missing = "not installed in this jail yet: its launcher installs it on first use"
				out = append(out, c)
				continue
			}
			prog, ok := progs[inst.Bin]
			if !ok {
				c.missing = "yolo's host floor holds no program named " + inst.Bin
				out = append(out, c)
				continue
			}
			st := floor.Status(prog)
			switch st.Disposition {
			case hostfloor.Provisioned:
				c.dir = st.Record.NpmPackageDir(prog.Install.Package)
				c.missing = "yolo's floor copy of it holds no installed " + name + " package"
			case hostfloor.Missing:
				c.missing = "not in yolo's host floor yet (" + st.Reason + "): the first `yolo host -- " +
					inst.Bin + "`, or `yolo host apply --assert`, installs it"
			default:
				c.missing = "no floor entry: " + st.Reason + ". `yolo host -- " + inst.Bin +
					"` runs the one on the PATH it is started with, whose catalog this check does not read"
			}
			out = append(out, c)
		}
	}
	return out
}

// listedModelIDs is every model id each composed provider lists, deduplicated and sorted per
// provider, and their total, less the providers reached only at a local address (MM-D20).
func listedModelIDs(providers *jsonx.OrderedMap) (map[string][]string, int) {
	out := map[string][]string{}
	total := 0
	for _, name := range providers.Keys() {
		entry := subMap(providers, name)
		if entry == nil || onlyLocalEndpoints(entry) {
			continue
		}
		models := subMap(entry, "models")
		if models == nil {
			continue
		}
		seen := map[string]bool{}
		for _, alias := range models.Keys() {
			v, _ := models.Get(alias)
			id, _ := v.(string)
			if om, ok := v.(*jsonx.OrderedMap); ok {
				id = str(om, "id")
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out[name] = append(out[name], id)
		}
		sort.Strings(out[name])
		total += len(out[name])
	}
	return out, total
}

// onlyLocalEndpoints reports whether a provider declares at least one endpoint and every one of
// them is on this machine or its host: `localhost`, a loopback address, or
// host.containers.internal, the name a jail reaches its host by. Such a provider is the user's
// own server (llama.cpp, ollama), whose model ids no vendor catalog lists (MM-D20).
func onlyLocalEndpoints(entry *jsonx.OrderedMap) bool {
	eps := subMap(entry, "endpoints")
	if eps == nil || eps.Len() == 0 {
		return false
	}
	for _, key := range eps.Keys() {
		u, err := url.Parse(str(subMap(eps, key), "base_url"))
		if err != nil || !localHost(u.Hostname()) {
			return false
		}
	}
	return true
}

func localHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "host.containers.internal":
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
