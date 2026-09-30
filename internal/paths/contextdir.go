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

// YOLO'S OWN CHILDREN OF THE CONTEXT DIR, on the container backends. Each is a bind the
// launch makes itself, so a context mount the user declares shares a namespace with them
// (docs/design/context-mounts.md §3.2), and the `yolo check` duplicate-destination error
// refuses a `mounts` element at, inside or containing one (config.validateMountDestinations):
// podman refuses two binds at one path, and for a nested pair it creates the inner mountpoint
// in the outer bind's HOST directory, even through a `:ro` bind. The run pipeline's own
// constants are spelled from these, so a bind and its reservation are one name.
const (
	// ContextPacksDir is where the staged pack trees are bound (YOLO_PACK_ROOT on podman).
	ContextPacksDir = ContainerContextDir + "/packs"
	// ContextCapturesDir is where the vendor-installer capture store is bound.
	ContextCapturesDir = ContainerContextDir + "/captures"
	// ContextHostUserDir is the root the source-bearing `host_files` entries are bound under.
	ContextHostUserDir = ContainerContextDir + "/host-user"
	// ContextHostNvimDir is where the host's ~/.config/nvim is bound for the entrypoint.
	ContextHostNvimDir = ContainerContextDir + "/host-nvim-config"
)

// ReservedContextPath is one of yolo's own children of the context dir, with what it holds.
type ReservedContextPath struct {
	Path, Holds string
}

// ReservedContextPaths lists yolo's own children of the context dir.
func ReservedContextPaths() []ReservedContextPath {
	return []ReservedContextPath{
		{ContextPacksDir, "the staged pack trees"},
		{ContextCapturesDir, "the vendor-installer capture store"},
		{ContextHostUserDir, "your host_files sources"},
		{ContextHostNvimDir, "your host nvim config"},
	}
}
