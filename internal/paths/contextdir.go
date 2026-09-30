package paths

// contextdir.go names the CONTEXT DIR to the agent: the directory context mounts appear
// under, which docs/design/context-mounts.md coins as a term (its Defined terms) and whose
// variable its CX-D4 decided.
//
// ONE SPELLING ON EVERY BACKEND is the whole point. A container binds context mounts under
// /ctx; macos-user cannot have a new top-level directory without /etc/synthetic.conf and a
// reboot, so its context dir is the root-owned staged tree instead
// (macosuser.StagedCtxRoot). Pack text and agents therefore write `$YOLO_CONTEXT_DIR/<rel>`,
// and a variable set on one backend only would make every reader branch.
//
// NOT YOLO_CTX_ROOT, which already means something else and keeps its meaning: that is
// where the ENTRYPOINT reads the host bytes it composes surfaces from, set only when a tree
// was staged — absent on podman, ~/.yolo-ctx on Apple Container. This one is agent-facing
// and set on EVERY launch, because the directory it names always exists (possibly empty),
// which is a deliberate departure from YOLO_PACK_ROOT's absence-is-the-signal rule.

// ContextDirEnv is the variable every launch exports naming the context dir.
const ContextDirEnv = "YOLO_CONTEXT_DIR"

// ContainerContextDir is the context dir on the container backends (podman and Apple
// Container): every context mount's default destination is a child of it.
const ContainerContextDir = "/ctx"
