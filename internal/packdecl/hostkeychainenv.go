package packdecl

// hostkeychainenv.go is the HOST KEYCHAIN ENV declaration (docs/design/keychain-from-a-jail.md
// OQ-KC5, ruled 2026-11-03): a program's login that lives in the user's host keychain, and the
// environment variable the program already reads in place of a keychain lookup.
//
// WHY A PACK FACT. On the macos-user backend a native client calls Security.framework on the
// sandbox account's default keychain, which does not exist, and there is no seam yolo can serve
// (keychain-from-a-jail.md §4.1). So at launch the HOST side, which runs as the human, reads the
// item from the human's keychain and writes it into that program's own agent env file. Which
// item, and which variable, are the program's facts: core knows no agent (AGENTS.md "Packs"), so
// the service names are declared here, never spelled in Go. packs/copilot declares
// `copilot-cli` → COPILOT_GITHUB_TOKEN.
//
// PACK-WIDE AND KEYED BY BIN, for provider_keys' reason (providerkeys.go): a new field on a
// `program` contribution makes an older yolo's tolerant reader skip the whole program, while it
// ignores an unknown top-level key.

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// HostKeychainEnv is one variable a program reads its login from, filled from the host
// keychain (Manifest.HostKeychainEnvByBin).
type HostKeychainEnv struct {
	// Var is the environment variable the program reads, COPILOT_GITHUB_TOKEN.
	Var string `json:"var"`
	// Services are the keychain items' service names, tried in order: the first item found is
	// the one passed. A list because a vendor may rename its item between releases and the
	// pack may name both while the new name is unverified.
	Services []string `json:"services"`
	// Login is the command a user runs on the host to create the item, "copilot login", named
	// by the launch line that finds none. Optional; without it the line names the program.
	Login string `json:"login,omitempty"`
}

// HostKeychainEnv is what the program installing bin declares, nil when it declares nothing or
// this manifest installs no such (non-fork) program. Copied, so a consumer cannot reach back
// into the manifest.
func (m *Manifest) HostKeychainEnv(bin string) []HostKeychainEnv {
	if m == nil || bin == "" || len(m.HostKeychainEnvByBin[bin]) == 0 {
		return nil
	}
	for _, c := range m.Contributions() {
		if c.Provides() && c.Bin == bin && !c.IsFork() {
			out := make([]HostKeychainEnv, len(m.HostKeychainEnvByBin[bin]))
			for i, e := range m.HostKeychainEnvByBin[bin] {
				out[i] = HostKeychainEnv{Var: e.Var, Services: append([]string(nil), e.Services...), Login: e.Login}
			}
			return out
		}
	}
	return nil
}

// HostKeychainBins is every bin this manifest declares host_keychain_env for, sorted.
func (m *Manifest) HostKeychainBins() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.HostKeychainEnvByBin))
	for bin := range m.HostKeychainEnvByBin {
		out = append(out, bin)
	}
	sort.Strings(out)
	return out
}

// validateHostKeychainEnv reports every structural problem with `host_keychain_env`, in the
// house `host_keychain_env["bin"][i].field: …` style.
func (m *Manifest) validateHostKeychainEnv() []string {
	var problems []string
	for _, bin := range m.HostKeychainBins() {
		label := fmt.Sprintf("host_keychain_env[%q]", bin)
		owned := false
		for _, c := range m.Contributions() {
			if bin != "" && c.Provides() && c.Bin == bin && !c.IsFork() {
				owned = true
				break
			}
		}
		if !owned {
			problems = append(problems, label+": must name a non-fork program declared by this pack; move the fact to the owning pack")
		}
		seenVar := map[string]bool{}
		for i, e := range m.HostKeychainEnvByBin[bin] {
			el := fmt.Sprintf("%s[%d]", label, i)
			if !ValidEnvName(e.Var) {
				problems = append(problems, fmt.Sprintf("%s.var: %q is not a valid environment variable "+
					"name ([A-Za-z_][A-Za-z0-9_]*) — name the variable the program reads its login from", el, e.Var))
			}
			if seenVar[e.Var] {
				problems = append(problems, fmt.Sprintf("%s.var: %q is declared twice for %q", el, e.Var, bin))
			}
			seenVar[e.Var] = true
			if len(e.Services) == 0 {
				problems = append(problems, el+".services: needs at least one keychain service name")
			}
			for j, s := range e.Services {
				if !plainKeychainWord(s) {
					problems = append(problems, fmt.Sprintf("%s.services[%d]: %q must be a non-empty "+
						"service name with no control characters and no leading '-'", el, j, s))
				}
			}
			if e.Login != "" && !plainKeychainWord(e.Login) {
				problems = append(problems, fmt.Sprintf("%s.login: %q must be a command line with no "+
					"control characters and no leading '-'", el, e.Login))
			}
		}
	}
	return problems
}

// plainKeychainWord is a non-empty string of printable characters that cannot be read as a
// flag: a service name is one argv word of `security`.
func plainKeychainWord(s string) bool {
	if strings.TrimSpace(s) == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
