package brokerscope

// GitHubHost is the one forge whose remote URLs a requested OWNER/REPO can be spelled as
// (RC-D26): the `remote_host` of the github source a project's `requests.github.repos` names.
const GitHubHost = "github.com"

// RequestedRepoURLs are the exact remote URLs a project's request for OWNER/REPO on host adds to a
// remote-url scope (docs/reference/config/rulings.md#RC-D26): the HTTPS URL and the scp-style SSH
// URL a fetch of it spells, each approved at the gate as a row of its own. nil for a host whose
// URLs are not known, which leaves the request unrepresentable there. The scope matches the
// `.git`, trailing-slash and relative/absolute-path spellings of these two itself
// (gitbroker.scopeContains); it matches no other host, user or port.
func RequestedRepoURLs(host, repo string) []string {
	if host != GitHubHost || !ValidRepo(repo) {
		return nil
	}
	return []string{"https://" + host + "/" + repo, "git@" + host + ":" + repo + ".git"}
}
