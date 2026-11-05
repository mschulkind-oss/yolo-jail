package packdecl

import (
	"fmt"
	"path"
	"strings"
)

// MCPAuth describes one pack's agent-neutral MCP sign-in store declaration
// (docs/design/mcp-auth-sharing.md §6). It is retained as schema metadata; a broker adapter
// may consume it when one is implemented.
type MCPAuth struct {
	Store                string        `json:"store"`
	Format               string        `json:"format"`
	EntriesAt            []string      `json:"entries_at,omitempty"`
	Companions           [][]string    `json:"companions,omitempty"`
	EmptyStore           string        `json:"empty_store,omitempty"`
	Lock                 *MCPAuthLock  `json:"lock,omitempty"`
	Fields               MCPAuthFields `json:"fields,omitempty"`
	RereadsBeforeRefresh bool          `json:"rereads_before_refresh,omitempty"`
	LoginHint            string        `json:"login_hint,omitempty"`
	UnsupportedReason    string        `json:"unsupported_reason,omitempty"`
}

// MCPAuthLock describes the lock an agent takes while changing its sign-in store.
type MCPAuthLock struct {
	Protocol     string `json:"protocol"`
	Path         string `json:"path,omitempty"`
	StaleSeconds int    `json:"stale_seconds,omitempty"`
}

// MCPAuthFields maps the sign-in fields in one native entry.
type MCPAuthFields struct {
	AccessToken  string             `json:"access_token,omitempty"`
	RefreshToken string             `json:"refresh_token,omitempty"`
	ExpiresAt    MCPAuthExpiryField `json:"expires_at,omitempty"`
	ClientID     string             `json:"client_id,omitempty"`
}

// MCPAuthExpiryField locates an expiry value and states its epoch unit.
type MCPAuthExpiryField struct {
	Path string `json:"path,omitempty"`
	Unit string `json:"unit,omitempty"`
}

func (c Contribution) MCPAuthDeclaration() *MCPAuth {
	if c.Kind != KindMCPAuth {
		return nil
	}
	return &MCPAuth{
		Store: c.MCPAuthStore, Format: c.MCPAuthFormat,
		EntriesAt:  append([]string(nil), c.MCPAuthEntriesAt...),
		Companions: cloneStringPaths(c.MCPAuthCompanions),
		EmptyStore: c.MCPAuthEmptyStore, Lock: c.MCPAuthLock, Fields: c.mcpAuthFields(),
		RereadsBeforeRefresh: c.MCPAuthRereadsBeforeRefresh,
		LoginHint:            c.MCPAuthLoginHint, UnsupportedReason: c.MCPAuthUnsupportedReason,
	}
}

func (c Contribution) mcpAuthFields() MCPAuthFields {
	if c.MCPAuthFields == nil {
		return MCPAuthFields{}
	}
	return *c.MCPAuthFields
}

func (c Contribution) hasMCPAuthFields() bool {
	fields := c.mcpAuthFields()
	return c.MCPAuthStore != "" || c.MCPAuthFormat != "" || c.MCPAuthEntriesAt != nil ||
		c.MCPAuthCompanions != nil || c.MCPAuthEmptyStore != "" || c.MCPAuthLock != nil ||
		c.MCPAuthFields != nil || fields.AccessToken != "" || fields.RefreshToken != "" ||
		fields.ExpiresAt.Path != "" || fields.ExpiresAt.Unit != "" ||
		fields.ClientID != "" || c.MCPAuthRereadsBeforeRefresh ||
		c.MCPAuthLoginHint != "" || c.MCPAuthUnsupportedReason != ""
}

func cloneStringPaths(paths [][]string) [][]string {
	if paths == nil {
		return nil
	}
	out := make([][]string, len(paths))
	for i := range paths {
		out[i] = append([]string(nil), paths[i]...)
	}
	return out
}

func (m *Manifest) projectMCPAuth() {
	m.MCPAuth = nil
	for _, contribution := range m.Contributes {
		if contribution.Kind == KindMCPAuth {
			m.MCPAuth = contribution.MCPAuthDeclaration()
			return
		}
	}
}

func mcpAuthProblems(label string, a *MCPAuth) []string {
	if a == nil {
		return []string{label + `: kind "mcp-auth" needs its store declaration`}
	}
	var problems []string
	if a.Store == "" {
		problems = append(problems, label+`.store: required — the home-relative sign-in store path`)
	} else {
		problems = appendPathProblems(problems, label+".store", a.Store)
		if clean := path.Clean(a.Store); clean != a.Store || strings.HasPrefix(a.Store, "~") || clean == "." {
			problems = append(problems, fmt.Sprintf("%s.store: %q must be a clean home-relative path", label, a.Store))
		}
	}
	switch a.Format {
	case "json", "dir", "unsupported":
	case "":
		problems = append(problems, label+`.format: required — use "json", "dir", or "unsupported"`)
	default:
		problems = append(problems, fmt.Sprintf(`%s.format: must be "json", "dir", or "unsupported", got %q`, label, a.Format))
	}
	switch a.EmptyStore {
	case "delete", "{}":
	case "":
		if a.Format != "unsupported" {
			problems = append(problems, label+`.empty_store: required — use "delete" or "{}"`)
		}
	default:
		problems = append(problems, fmt.Sprintf(`%s.empty_store: must be "delete" or "{}", got %q`, label, a.EmptyStore))
	}
	if a.Format == "unsupported" {
		if strings.TrimSpace(a.UnsupportedReason) == "" {
			problems = append(problems, label+`.unsupported_reason: required when format is "unsupported"`)
		}
	} else if a.UnsupportedReason != "" {
		problems = append(problems, label+`.unsupported_reason: applies only when format is "unsupported"`)
	}
	if a.Format != "unsupported" {
		if a.Lock == nil {
			problems = append(problems, label+`.lock: required for a supported store format`)
		} else {
			problems = append(problems, validateMCPAuthLock(label+".lock", *a.Lock)...)
		}
	} else if a.Lock != nil {
		problems = append(problems, label+`.lock: applies only to supported store formats`)
	}
	if a.Fields.ExpiresAt.Unit != "" && a.Fields.ExpiresAt.Unit != "ms" && a.Fields.ExpiresAt.Unit != "s" {
		problems = append(problems, fmt.Sprintf(`%s.fields.expires_at.unit: must be "ms" or "s", got %q`, label, a.Fields.ExpiresAt.Unit))
	}
	if (a.Fields.ExpiresAt.Path == "") != (a.Fields.ExpiresAt.Unit == "") {
		problems = append(problems, label+`.fields.expires_at: path and unit must be declared together`)
	}
	return problems
}

func validateMCPAuthLock(label string, lock MCPAuthLock) []string {
	var problems []string
	switch lock.Protocol {
	case "flock", "proper-lockfile", "mkdir-heartbeat", "none":
	case "":
		problems = append(problems, label+`.protocol: required — use "flock", "proper-lockfile", "mkdir-heartbeat", or "none"`)
	default:
		problems = append(problems, fmt.Sprintf(`%s.protocol: must be "flock", "proper-lockfile", "mkdir-heartbeat", or "none", got %q`, label, lock.Protocol))
	}
	if lock.Protocol != "none" && lock.Path == "" {
		problems = append(problems, label+`.path: required for a named lock protocol`)
	}
	if lock.Protocol == "none" && lock.Path != "" {
		problems = append(problems, label+`.path: applies only to a named lock protocol`)
	}
	if lock.Protocol == "proper-lockfile" && lock.StaleSeconds <= 0 {
		problems = append(problems, label+`.stale_seconds: required and must be positive for proper-lockfile`)
	}
	if lock.Protocol != "proper-lockfile" && lock.StaleSeconds != 0 {
		problems = append(problems, label+`.stale_seconds: applies only to proper-lockfile`)
	}
	if lock.StaleSeconds < 0 {
		problems = append(problems, label+`.stale_seconds: must not be negative`)
	}
	if lock.Path != "" {
		problems = appendPathProblems(problems, label+".path", lock.Path)
		if clean := path.Clean(lock.Path); clean != lock.Path || strings.HasPrefix(lock.Path, "~") || clean == "." {
			problems = append(problems, fmt.Sprintf("%s.path: %q must be a clean home-relative path", label, lock.Path))
		}
	}
	return problems
}
