package releasematrix

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"
)

// ProgramDir is the directory an official binary is built from, relative to the checkout: its
// `binaries` key under cmd/ (BP-D7).
func ProgramDir(name string) string { return "cmd/" + name }

// EmbedChain reports whether cmd/<name>, built for goos/goarch, links the packs embed, and by
// which chain of imports: the program first, EmbedPackage last. nil when it does not.
//
// It walks this module's own packages with go/build and the platform's build constraints, cgo
// off as the recipe builds, following only imports under ModulePath: std and vendor/ cannot
// import this module. Test files are not read, because a test's imports are never linked.
// Nothing is compiled, so the short suite can ask this of every official binary on every
// platform.
//
// The error is non-nil when cmd/<name> is not a Go main package buildable for the platform,
// which is its own refusal: an official binary is a program in this module (BP-D7).
func EmbedChain(root, name, goos, goarch string) ([]string, error) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = goos, goarch, false

	dir := filepath.Join(root, filepath.FromSlash(ProgramDir(name)))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("there is no %s: an official binary is built from this module's "+
			"%s, named for its `binaries` key", ProgramDir(name), ProgramDir("<name>"))
	}
	start := ModulePath + "/" + ProgramDir(name)
	parent := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == EmbedPackage {
			var chain []string
			for p := cur; p != ""; p = parent[p] {
				chain = append([]string{p}, chain...)
			}
			return chain, nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(cur, ModulePath), "/")
		pkg, err := ctx.ImportDir(filepath.Join(root, filepath.FromSlash(rel)), 0)
		if err != nil {
			var noGo *build.NoGoError
			if cur == start && errors.As(err, &noGo) {
				return nil, fmt.Errorf("%s has no Go file built for %s/%s", ProgramDir(name),
					goos, goarch)
			}
			return nil, fmt.Errorf("reading %s for %s/%s: %w", cur, goos, goarch, err)
		}
		if cur == start && pkg.Name != "main" {
			return nil, fmt.Errorf("%s is package %s, not a program (package main)",
				ProgramDir(name), pkg.Name)
		}
		for _, imp := range pkg.Imports {
			if imp != ModulePath && !strings.HasPrefix(imp, ModulePath+"/") {
				continue
			}
			if _, seen := parent[imp]; seen {
				continue
			}
			parent[imp] = cur
			queue = append(queue, imp)
		}
	}
	return nil, nil
}
