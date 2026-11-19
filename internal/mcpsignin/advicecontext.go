package mcpsignin

import "strings"

// AdviceContext is frozen user-selection metadata, not sign-in authority. An
// older launch with no context derives no candidates and remains readable.
type AdviceContext struct {
	Version          int      `json:"version"`
	MappingReadable  bool     `json:"mapping_readable"`
	Formats          []string `json:"formats,omitempty"`
	SelectedEmbedded []string `json:"selected_embedded,omitempty"`
	ShadowNames      []string `json:"shadow_names,omitempty"`
}

func (a *AdviceContext) Valid() bool {
	if a == nil || a.Version != 1 || len(a.Formats) > 32 || len(a.SelectedEmbedded) > 256 || len(a.ShadowNames) > 256 {
		return false
	}
	for _, format := range a.Formats {
		if format != "claude-plugin" {
			return false
		}
	}
	for _, names := range [][]string{a.SelectedEmbedded, a.ShadowNames} {
		for _, name := range names {
			if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\\\x00\r\n") || name == "." || name == ".." {
				return false
			}
		}
	}
	return true
}
