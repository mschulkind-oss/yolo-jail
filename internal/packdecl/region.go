package packdecl

import "fmt"

// RegionProblem reports what is wrong with a provider's `region` at path, "" when nothing is.
//
// A REGION BECOMES PART OF A HOST NAME. The agents that read it compose their service's address
// from it by string template, with no check of their own: Claude Code and opencode 1.18.32 both
// build `https://bedrock-runtime.${region}.amazonaws.com` (read statically from their shipped
// binaries, 2026-09-29, never run). So a value carrying a dot, a slash or a "#" is not a region
// but a host: "attacker.example/#" sends every request on the provider, and the credential
// beside it, to bedrock-runtime.attacker.example. And a workspace config may set a region
// (config.validateProviderCredentialScope keeps it mergeable), which is a file the jailed agent
// can edit.
//
// So a region is ONE DNS LABEL: lowercase letters and digits, with single hyphens between them,
// at most 63 characters. Every AWS region id has that shape ("us-east-1", "us-gov-west-1",
// "eusc-de-east-1"), as do other clouds' ("us-central1", "eastus2"), and a label can name a host
// only inside the domain the agent appends to it. Only the SHAPE is core's: core keeps no list of
// regions, since which ones exist, and which serve which model, is AWS's to change.
//
// Read by two layers, declared once: a pack's provider `region` (validateContribution) and a
// user's `providers.<name>.region` at either config scope (config.validateProviderEntries and
// config.validateProviderCredentialScope).
func RegionProblem(path, v string) string {
	if !validRegion(v) {
		return fmt.Sprintf("%s: %q is not a region: a region is one DNS label (lowercase letters "+
			"and digits, with hyphens between them, such as \"us-east-1\"), because an agent builds "+
			"its service's host name from it, and a dot, a slash or a \"#\" would send its requests "+
			"and credential to another host", path, v)
	}
	return ""
}

// validRegion reports whether s is one DNS label in lowercase: [a-z0-9]+(-[a-z0-9]+)*, at most 63
// bytes.
func validRegion(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && s[i-1] != '-':
		default:
			return false
		}
	}
	return true
}
