package gitproto

// PushProposal is the version-1 access envelope's ordered candidate from a client
// dry-run. Old IDs are untrusted observations; the host verifies them from its own
// receive-pack advertisement before building the ACT push request. PushOptions are
// effective options in actual Git wire order, not just options typed on the CLI.
type PushProposal struct {
	Service     string    `json:"service"`
	URL         string    `json:"url"`
	Commands    []Command `json:"commands"`
	PushOptions []string  `json:"push_options"`
}
