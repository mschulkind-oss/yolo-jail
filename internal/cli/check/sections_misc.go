package check

import "os"

// sectionScope names the commands that own disk accounting and reclaim. The broad check no longer
// walks host storage or workspace trees as a side effect of config/environment diagnosis.
func (o *Options) sectionScope(r *reporter) {
	r.sectionHeader("Scope")
	r.dim("Disk inventory: `yolo stores`; reclaim candidates and cleanup: `yolo prune`")
	r.blank()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
