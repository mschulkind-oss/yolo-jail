package paths

// contextdir.go names the CONTEXT DIR: the directory context mounts appear under, which
// docs/design/context-mounts.md coins as a term (its Defined terms).

// ContainerContextDir is the context dir on the container backends (podman and Apple
// Container): every context mount's default destination is a child of it.
const ContainerContextDir = "/ctx"
