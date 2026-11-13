package config

// envsecretsrefusal.go words the two refusals of the two env sets
// (docs/reference/providers.md#the-two-env-sets-public-and-private): a selected
// provider's key in the public set (row 2), and one name in both sets (SE-D2). Both are fatal and
// have no hatch (SE-D10); every launch path, `yolo host` and `yolo check` call this one function,
// so they cannot word it differently. Names and places only, never a value.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// SecretEnvRefusals is every refusal h earns, in the order SEP-D8 and SEP-D17 set: one line per
// dotenv file listed in both sets, then one per other name in both sets, then one per public
// name a selected provider claims (claims, from CredentialScope.PublicClaims). site answers the
// scope flag and format of the config that set name in the given set, for the remedy's verb.
// Empty when there is nothing to refuse.
func SecretEnvRefusals(h *HydratedEnv, claims []packload.PublicClaim, site func(name string, private bool) EnvSite) []string {
	var out []string
	both := map[string]bool{}
	sharedDone := map[string]bool{}
	var perName []string
	for _, name := range h.BothSets() {
		both[name] = true
		places := h.Places(name)
		if len(places) != 2 {
			continue
		}
		pub, prv := places[0], places[1]
		if pub.Entry != "" && prv.Entry != "" && (pub.Entry == prv.Entry ||
			SameDotenvFile(EnvSourcePathByName(pub.Entry, ""), EnvSourcePathByName(prv.Entry, ""))) {
			if !sharedDone[pub.Entry] {
				sharedDone[pub.Entry] = true
				out = append(out, sharedFileRefusal(pub.Entry, sharedFileSite(site(name, false), pub.Entry)))
			}
			continue
		}
		perName = append(perName, bothSetsRefusal(name, pub, prv, site(name, false), site(name, true)))
	}
	out = append(out, perName...)
	for _, c := range claims {
		if both[c.Name] {
			continue
		}
		// Places lists the public place first; a name only the private set holds earns nothing.
		places := h.Places(c.Name)
		if len(places) == 0 || places[0].Private {
			continue
		}
		out = append(out, publicKeyRefusal(c, places[0], site(c.Name, false)))
	}
	return out
}

// sharedFileSite is the site whose scope flag a shared-file remedy carries: the one that lists
// entry. A name's own public site may be another scope's (an inline value, or another file), and
// its flag would point the verb at the wrong config; with none that lists entry, no flag is
// named and the verb asks which scope.
func sharedFileSite(s EnvSite, entry string) EnvSite {
	if s.Place.Key != "" && s.Place.Entry != entry {
		return EnvSite{}
	}
	return s
}

// asWritten is p as the user's file spells it: the load lowers a legacy `env_sources` to the
// place key env.sources, and a refusal over a legacy file names the key that file holds (SEP-D7).
func asWritten(p EnvPlace, s EnvSite) EnvPlace {
	if s.Legacy && p.Key == "env.sources" {
		p.Key = "env_sources"
	}
	return p
}

// refusalLabel is the config key a public place was written under, as a refusal's prefix.
func refusalLabel(p EnvPlace) string {
	if p.Key == "env_sources" {
		return "config.env_sources"
	}
	return "config." + p.Key
}

// remedyCommand is argv as the user should run it from where they are: after `yolo config
// migrate` for a legacy file (SE-D6, SEP-D18), and through the host-run line for a user-scope
// edit inside a jail.
func remedyCommand(s EnvSite, argv ...string) string {
	if s.Scope != "" {
		argv = append(argv, s.Scope)
	}
	cmd := shquote.Join(append([]string{"yolo", "config"}, argv...))
	if InJail() && s.Scope == EnvScopeUser {
		cmd = "yolo host-run -C " + shquote.QuoteDisplay(JailWorkspace()) + " --name config-secret -- " + cmd
	}
	if s.Legacy {
		return "run yolo config migrate, then " + cmd
	}
	return cmd
}

func publicKeyRefusal(c packload.PublicClaim, p EnvPlace, s EnvSite) string {
	p = asWritten(p, s)
	claim := "the " + c.Providers[0] + " provider this launch selects claims it as its key"
	if len(c.Providers) > 1 {
		claim = "the " + strings.Join(c.Providers, ", ") + " providers this launch selects claim it as their key"
	}
	return refusalLabel(p) + ": " + c.Name + " is set in your public environment " + p.Describe() + ", and " +
		claim + ". A public value reaches every process in the jail, so a provider's key may not live there. " +
		"Move it to the private set: " + remedyCommand(s, "secret", c.Name)
}

func bothSetsRefusal(name string, pub, prv EnvPlace, pubSite, prvSite EnvSite) string {
	pub = asWritten(pub, pubSite)
	return "config.env: " + name + " is set in both your public environment " + pub.Describe() +
		" and your private one " + prv.Describe() + ". A name lives in one set. Keep the private copy:\n" +
		remedyCommand(pubSite, "secret", name, "--drop") + "\n" +
		"or keep the public copy: " + remedyCommand(prvSite, "unsecret", name, "--drop")
}

func sharedFileRefusal(entry string, s EnvSite) string {
	return "config.env: " + entry + " is listed in both [env] files and [env.secrets] files, so every name in it " +
		"is in both sets. A file lives in one set. Make it private:\n" +
		remedyCommand(s, "secret", "--dotenv", entry) + "\n" +
		"or keep it public: " + remedyCommand(s, "unsecret", "--dotenv", entry)
}
