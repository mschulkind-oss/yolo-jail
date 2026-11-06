package configmigrate

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// brokerGrantsLayer is the equality check's layer for what the user config grants each broker:
// the grants the brokers resolve, whichever spelling says them.
const brokerGrantsLayer = "the grants the brokers resolve"

var shipped struct {
	once         sync.Once
	declarations []config.ReadableBrokerDeclaration
	err          error
}

// shippedBrokers are the broker declarations of every shipped pack: the one place the mapping
// between a broker's legacy grant kinds and its `allow.<source>` capabilities is written. Core
// names no broker; a user brokered entry converts through its declaring pack's own words.
func shippedBrokers() ([]config.ReadableBrokerDeclaration, error) {
	shipped.once.Do(func() {
		shipped.declarations, _, shipped.err = config.ReadableDeclarationsForPacks(packload.Embedded())
	})
	return shipped.declarations, shipped.err
}

// brokerGrants is what user, one side's user config as the readers made it, grants each shipped
// broker for every workspace, read the way the brokers read it (config.UserBrokerAuthorityOf):
// the legacy `brokered` readers where the config has no `allow.<source>` table, the readable
// resolver where it has one. The per-scope layers compare the lowered spelling; this compares
// what the brokers make of it.
func brokerGrants(user *jsonx.OrderedMap, declarations []config.ReadableBrokerDeclaration) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, a := range config.UserBrokerAuthorityOf(user, selectedPackNames(user), declarations) {
		entry := jsonx.NewOrderedMap()
		entry.Set("read", a.Read)
		kinds := make([]any, 0, len(a.GrantKinds))
		for _, k := range a.GrantKinds {
			kinds = append(kinds, k)
		}
		entry.Set("grant_kinds", kinds)
		if a.Problem != "" {
			entry.Set("problem", a.Problem)
		}
		out.Set(a.Source, entry)
	}
	// The permission sets each source hands over, whichever spelling wrote them (RC-D54).
	for _, d := range declarations {
		source := d.Brokered.Source
		sets, problems := config.BrokeredSetsOf(user, source)
		if len(sets) == 0 && len(problems) == 0 {
			continue
		}
		v, _ := out.Get(source)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			entry = jsonx.NewOrderedMap()
			out.Set(source, entry)
		}
		if _, done := entry.Get("sets"); done {
			continue
		}
		rendered := make([]any, 0, len(sets))
		for _, set := range sets {
			rendered = append(rendered, fmt.Sprintf("%+v", set))
		}
		entry.Set("sets", rendered)
		if len(problems) > 0 {
			entry.Set("sets_problems", strings.Join(problems, "; "))
		}
	}
	return out
}

// lowerUserBrokered converts a user file's brokered table to the ruled `allow.<source>` tables
// (config-user.md §6 and appendix A.6): the read grant on "*" becomes the source's read
// capability under `always`, and `grant_kinds` becomes `on_request`, each kind through the
// capability the declaring pack maps it to (config.LegacyUserAllowance, the mapping the readable
// launch already applies to a legacy user config). Anything the declaration cannot say stays at
// its old spelling with the next step named.
func lowerUserBrokered(sources *jsonx.OrderedMap, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	declarations, err := shippedBrokers()
	for _, source := range sources.Keys() {
		sv, _ := sources.Get(source)
		path := []string{"brokered", source}
		table, ok := sv.(*jsonx.OrderedMap)
		switch {
		case !ok:
			skip(path, sv, "a broker's entry must be an object")
			continue
		case err != nil:
			skip(path, sv, "the shipped broker declarations could not be read ("+err.Error()+"); "+noSpellingYet)
			continue
		}
		declaration, reason := userBrokerDeclaration(declarations, source, ctx.view())
		if reason != "" {
			skip(path, sv, reason)
			continue
		}
		convertible := jsonx.NewOrderedMap()
		for _, key := range table.Keys() {
			v, _ := table.Get(key)
			at := append(append([]string(nil), path...), key)
			problem := ""
			switch key {
			case "grants":
				problem = userReadGrantsProblem(source, v)
			case "grant_kinds":
				problem = userGrantKindsProblem(v, declaration)
			case config.AllowSetsKey:
				// The list moves whole, entry for entry, to allow.<source>.sets (RC-D54).
				if _, problems := config.BrokeredSetsOf(legacySets(source, v), source); len(problems) > 0 {
					skip(at, v, strings.Join(problems, "; "))
				} else if !place([]string{"allow", source, config.AllowSetsKey}, v, at) {
					skip(at, v, "allow."+source+"."+config.AllowSetsKey+" is already written in this file; keep one of the two")
				}
				continue
			default:
				d, _ := renameDescriptorFor(at)
				problem = unconvertedReason(d)
			}
			if problem != "" {
				skip(at, v, problem)
				continue
			}
			convertible.Set(key, v)
		}
		only := jsonx.NewOrderedMap()
		only.Set(source, convertible)
		legacy := jsonx.NewOrderedMap()
		legacy.Set("brokered", only)
		allow := config.LegacyUserAllowance(legacy, declaration)
		for _, key := range allow.Keys() {
			v, _ := allow.Get(key)
			oldKey := "grants"
			if key == "on_request" {
				oldKey = "grant_kinds"
			}
			oldPath := append(append([]string(nil), path...), oldKey)
			if !place([]string{"allow", source, key}, v, oldPath) {
				old, _ := convertible.Get(oldKey)
				skip(oldPath, old, "allow."+source+"."+key+" is already written in this file; keep one of the two")
			}
		}
	}
}

// userBrokerDeclaration is the declaration of the broker named source by a pack the user config
// selects, or why there is none to convert through: an `allow.<source>` table grants only while
// its declaring pack is in packs.
func userBrokerDeclaration(declarations []config.ReadableBrokerDeclaration, source string, view *userView) (config.ReadableBrokerDeclaration, string) {
	var declaring, selected []config.ReadableBrokerDeclaration
	for _, d := range declarations {
		if d.Brokered.Source != source {
			continue
		}
		declaring = append(declaring, d)
		if view.packs[d.Pack] {
			selected = append(selected, d)
		}
	}
	switch {
	case len(selected) == 1:
		return selected[0], ""
	case len(selected) > 1:
		return config.ReadableBrokerDeclaration{}, "more than one selected pack declares the " + source + " broker; keep one of them in packs, then run `yolo config migrate` again"
	case len(declaring) == 1:
		pack := declaring[0].Pack
		return config.ReadableBrokerDeclaration{}, "[allow." + source + "] grants only while " + pack + ", the pack declaring the broker, is in packs; add it with `yolo pack add " + pack + "`, then run `yolo config migrate` again"
	case len(declaring) > 1:
		return config.ReadableBrokerDeclaration{}, "no selected pack declares the " + source + " broker, and more than one shipped pack does; add the one you use with `yolo pack add`, then run `yolo config migrate` again"
	default:
		return config.ReadableBrokerDeclaration{}, "no shipped pack declares a broker named " + source + ", so its [allow." + source + "] capabilities are not known here; " + noSpellingYet
	}
}

// userReadGrantsProblem is why a user `grants` list has no `always` spelling, "" when it is
// exactly the read grant on "*" (or empty): the one grant a user config holds (BB-D107).
func userReadGrantsProblem(source string, v any) string {
	list, ok := v.([]any)
	if !ok {
		return "grants must be a list"
	}
	elsewhere := "a grant naming a repository or a ref belongs to the project that needs it, as `yolo request " + source + " …` in its workspace (`yolo request --help` names each capability); move it there, then run `yolo config migrate` again"
	if len(list) > 1 {
		return "the user config holds one grant, {\"kind\": \"read\", \"repo\": \"*\"}, listed once; " + elsewhere
	}
	for _, e := range list {
		m, ok := e.(*jsonx.OrderedMap)
		if !ok {
			return "a grant must be an object"
		}
		kind, _ := m.Get("kind")
		repo, _ := m.Get("repo")
		if m.Len() != 2 || kind != "read" || repo != "*" {
			return "only the read grant on every workspace's own remotes, {\"kind\": \"read\", \"repo\": \"*\"}, has a user-config spelling; " + elsewhere
		}
	}
	return ""
}

// userGrantKindsProblem is why a user `grant_kinds` list has no `on_request` spelling, "" when
// every kind maps to a built capability of the declaring pack.
func userGrantKindsProblem(v any, declaration config.ReadableBrokerDeclaration) string {
	list, ok := v.([]any)
	if !ok {
		return "grant_kinds must be a list of grant kinds"
	}
	pending := map[string]bool{}
	for _, c := range declaration.PendingCapabilities {
		pending[c] = true
	}
	var maps []string
	for _, kind := range declaration.Brokered.Grants {
		if c := declaration.KindCapabilities[kind]; c != "" && !pending[c] {
			maps = append(maps, kind+" → "+c)
		}
	}
	for _, e := range list {
		kind, ok := e.(string)
		if !ok {
			return "grant_kinds must be a list of grant kinds"
		}
		capability := declaration.KindCapabilities[brokerscope.CanonicalGrantKind(kind)]
		switch {
		case capability == "":
			return describe(kind) + " is not a grant kind the " + declaration.Pack + " pack maps to a capability (it maps " + strings.Join(maps, ", ") + "); " + noSpellingYet
		case pending[capability]:
			return describe(kind) + " maps to " + capability + ", which the " + declaration.Pack + " pack has not built yet; " + noSpellingYet
		}
	}
	return ""
}

// selectedPackNames are the pack names user lists in `packs`.
func selectedPackNames(user *jsonx.OrderedMap) []string {
	var out []string
	for name := range newUserView(user).packs {
		out = append(out, name)
	}
	return out
}

// legacySets is a user config holding only source's legacy sets list v.
func legacySets(source string, v any) *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	entry.Set(config.AllowSetsKey, v)
	sources := jsonx.NewOrderedMap()
	sources.Set(source, entry)
	out := jsonx.NewOrderedMap()
	out.Set("brokered", sources)
	return out
}
