//go:build darwin

package entrypoint

// maxLinkHops is XNU's MAXSYMLINKS: one path resolution follows at most 32 symlinks before it
// fails with ELOOP. With Linux's 40 here, a 33-to-40-link chain the walk called finite was
// then refused by the kernel on the write ("too many levels of symbolic links") instead of
// being refused by the walk with its "cannot prove" next step. See hostbrokenlink.go.
const maxLinkHops = 32
