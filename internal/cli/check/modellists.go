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
// ($NPM_CONFIG_PREFIX/lib/node_modules/<package>, where its launcher installs it), at the host
// the host floor's copy, which is what `yolo host -- <agent>` runs (host-tool-provisioning.md).
func (o *Options) declaredCatalogs(packs []*packload.Pack) []declaredCatalog {
	var out []declaredCatalog
	var records map[string]*hostfloor.Record
	inJail := o.getenv("YOLO_VERSION") != ""
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.Kind != "npm" || len(in.ModelCatalog) == 0 {
				continue
			}
			c := declaredCatalog{name: in.Bin, globs: in.ModelCatalog}
			name, _ := packdecl.SplitNpmSpec(in.Package)
			switch {
			case inJail:
				prefix := o.getenv("NPM_CONFIG_PREFIX")
				if prefix == "" {
					c.missing = "this jail names no npm prefix (NPM_CONFIG_PREFIX), so there is nowhere to look"
					break
				}
				c.dir = filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(name))
				c.missing = "not installed in this jail yet: its launcher installs it on first use"
			default:
				if records == nil {
					records = o.hostFloor(nil).Records()
				}
				c.dir = records[in.Bin].NpmPackageDir(in.Package)
				c.missing = "yolo's host floor holds no copy of it yet: the first `yolo host -- " +
					in.Bin + "`, or `yolo host apply --assert`, installs one"
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
