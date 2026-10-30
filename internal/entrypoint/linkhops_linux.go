//go:build linux

package entrypoint

// maxLinkHops is Linux's MAXSYMLINKS: one path resolution follows at most 40 symlinks before
// it fails with ELOOP. See hostbrokenlink.go for what the bound guards.
const maxLinkHops = 40
