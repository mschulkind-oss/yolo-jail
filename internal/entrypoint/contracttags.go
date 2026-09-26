package entrypoint

// ContractTagsEnv carries a jail's CONTRACT TAGS: the comma-separated names of what this jail's
// frozen binaries can receive from a LATER attach (docs/design/attach-skew-and-contract-guardrails.md,
// OQ-SK2, which ruled named tags over a version counter; "contract tag" is that doc's term).
//
// The host launcher that creates a container freezes it into the container's environment,
// and nothing in the jail reads it. It is for the NEXT host yolo that attaches: a container's
// frozen environment is the one record of its launch an attach can inspect, and a running
// jail keeps the binaries it launched with (the mounted prefix is immutable), so an attach
// from a newer yolo must ask what this jail can take before handing it anything. The
// vocabulary and both halves of the comparison live in internal/cli/run (contracttags.go),
// the only reader and the only writer; the name lives here beside the other launcher-to-jail
// variables, AgentEnvFilesEnv among them — the legacy marker this list replaced.
const ContractTagsEnv = "YOLO_CONTRACT_TAGS"
