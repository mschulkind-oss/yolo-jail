package paths

// ScratchSlot is one scratch mount: the name segment its podman volume carries and the
// directory in the jail it backs (internal/prune/scratchvolumes.go owns the volumes).
type ScratchSlot struct {
	Name string
	Dest string
}

// ScratchSlots is the ordered set of disk-backed scratch mounts a podman jail gets under
// `ephemeral_storage: "volume"`: its per-launch directories. prune.ScratchSlots is this list;
// it lives here, in a leaf package, so the in-jail durable-dir report can say which worktree
// registrations point into the per-launch set without linking the reaper into
// yolo-entrypoint.
var ScratchSlots = []ScratchSlot{
	{Name: "tmp", Dest: "/tmp"},
	{Name: "var-tmp", Dest: "/var/tmp"},
	{Name: "var-lib-containers", Dest: "/var/lib/containers"},
	{Name: "var-cache-containers", Dest: "/var/cache/containers"},
}

// ScratchDests is the destinations of ScratchSlots, in order.
func ScratchDests() []string {
	out := make([]string, 0, len(ScratchSlots))
	for _, s := range ScratchSlots {
		out = append(out, s.Dest)
	}
	return out
}
