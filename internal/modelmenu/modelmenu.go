// Package modelmenu writes a program's MODEL MENU (packdecl.ModelMenu, a term coined there): the
// program's own catalog entries whose id yolo's list names, in the list's order, with the fields
// the pack declares renumbered, renamed, cleared or set. Two notches run it, through one build
// (Request.write; docs/design/model-lists-and-pickers.md MM-D26):
//
//   - in a jail, the generated launcher runs it before exec'ing the program, as `yolo internal
//     model-menu` (Run; MM-D9, MM-D22), reading the list the boot rendered into the home and
//     writing the menu at the one path the pack declares;
//   - at the host, `yolo host --` hands it the list the launch composed (MM-D24) and a directory
//     of yolo's state (Request.WriteIn), where each menu is named by its cache key and kept for as
//     long as a running program holds it (MM-D27).
//
// Either way the caller adds the pack's flag naming the file only when a menu was written.
//
// It knows no program. Every key it reads and writes is the pack's declaration, so codex's
// `slug`, `priority` and `upgrade` are words packs/codex states, not words this package spells.
package modelmenu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Verb is the `yolo internal` verb the generated launcher runs this package as. Spelled here and
// read by both the launcher template (internal/entrypoint) and the dispatcher (internal/cli), so
// the two cannot drift.
const Verb = "model-menu"

// Bounds on one run. codex 0.159.2's bundled catalog is 11 entries of 20 to 65 KB (MEASURED
// 2026-09-30), so both are far above a real catalog and exist only so a program that hangs or
// floods cannot hold the launch the user typed.
const (
	catalogTimeout  = 30 * time.Second
	maxCatalogBytes = 64 << 20
)

// ListEntry is one model of yolo's list, as the pack's derive renders it.
type ListEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Result is what one projection made of a catalog.
type Result struct {
	// Menu is the menu file's bytes, nil when no listed id survived.
	Menu []byte
	// Kept is the listed ids the menu holds, in its order.
	Kept []string
	// Missing is the listed ids the program's catalog lacks, in the list's order.
	Missing []string
}

// Project keeps the entries of catalog, a JSON object, whose spec.ID names a model of list, in
// the list's order, and applies spec's renumbering, names, clears and sets to each. Every other
// key of the catalog object and of each kept entry is copied as it was, so the entry keeps the
// prompt text the program needs. An error means the catalog is not the shape spec describes.
func Project(catalog []byte, list []ListEntry, spec packdecl.ModelMenu) (Result, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &top); err != nil {
		return Result{}, fmt.Errorf("its output is not a JSON object: %v", err)
	}
	raw, ok := top[spec.Entries]
	if !ok {
		return Result{}, fmt.Errorf("its output has no %q key", spec.Entries)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return Result{}, fmt.Errorf("its %q is not an array of objects: %v", spec.Entries, err)
	}
	byID := make(map[string]map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		var id string
		if err := json.Unmarshal(e[spec.ID], &id); err != nil || id == "" {
			continue
		}
		if _, dup := byID[id]; !dup {
			byID[id] = e
		}
	}
	var res Result
	var kept []map[string]json.RawMessage
	seen := map[string]bool{}
	for _, l := range list {
		if l.ID == "" || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		e, ok := byID[l.ID]
		if !ok {
			res.Missing = append(res.Missing, l.ID)
			continue
		}
		out := make(map[string]json.RawMessage, len(e)+len(spec.Set)+2)
		for k, v := range e {
			out[k] = v
		}
		if spec.Order != "" {
			out[spec.Order] = json.RawMessage(fmt.Sprint(len(kept)))
		}
		if spec.Name != "" && l.Name != "" {
			out[spec.Name] = mustString(l.Name)
		}
		for _, k := range spec.Clear {
			delete(out, k)
		}
		for k, v := range spec.Set {
			out[k] = mustString(v)
		}
		kept = append(kept, out)
		res.Kept = append(res.Kept, l.ID)
	}
	if len(kept) == 0 {
		return res, nil
	}
	arr, err := json.Marshal(kept)
	if err != nil {
		return Result{}, err
	}
	top[spec.Entries] = arr
	menu, err := json.Marshal(top)
	if err != nil {
		return Result{}, err
	}
	res.Menu = menu
	return res, nil
}

func mustString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// ReadList reads yolo's list from the file a jail's boot rendered it into: nil for a missing,
// unparseable or empty file, which all mean "no menu this launch".
func ReadList(path string) []ListEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseList(data)
}

// ParseList is ReadList's reader over the list's bytes, `{"models": [{"id": …, "name": …}]}`, for
// a list that never reached a file: the one `yolo host --` composes for its own launch (MM-D24).
// nil for bytes that do not parse or name no model. One reader, so a list reads the same whichever
// notch composed it.
func ParseList(data []byte) []ListEntry {
	var doc struct {
		Models []ListEntry `json:"models"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var out []ListEntry
	for _, m := range doc.Models {
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

// Flag is spec.Flag with packdecl.ModelMenuInto replaced by into, the menu's absolute path.
func Flag(spec packdecl.ModelMenu, into string) []string {
	out := make([]string, len(spec.Flag))
	for i, w := range spec.Flag {
		out[i] = strings.ReplaceAll(w, packdecl.ModelMenuInto, into)
	}
	return out
}

// Request is ONE BUILD of a program's menu, with everything the build reads, whichever notch
// asks for it (docs/design/model-lists-and-pickers.md MM-D26): the jail's `yolo internal
// model-menu` fills List from the file its boot rendered, and `yolo host --` from the list its
// launch composed. The catalog run, the projection, the cache key and the missing-id warning are
// this type's, so the two notches cannot build two different menus from one list.
type Request struct {
	// Bin is the program's name, for the lines the build prints.
	Bin string
	// Spec is the program's pack's declaration.
	Spec packdecl.ModelMenu
	// List is yolo's list for the launch's provider, in menu order. Empty is "no menu".
	List []ListEntry
	// Program is the argv that runs the program, its resolved file last: the jail launcher's
	// interpreter prefix, if any, then the binary. The catalog is Program with Spec.Catalog
	// appended, and the last word's file is the program half of the cache key.
	Program []string
	// Env is the catalog run's environment, nil for this process's own.
	Env []string
	// Prefix begins every line the build prints: "yolo: " when empty, the jail launcher's
	// spelling, and "yolo host: " at the host, whose lines all name that notch.
	Prefix string
}

func (r Request) prefix() string {
	if r.Prefix == "" {
		return "yolo: "
	}
	return r.Prefix
}

// Key is the request's CACHE KEY: the hash of the declaration, the list and the program file's
// identity, by which a menu already written is reused without running the program (MM-D22) and
// by which the host names each menu it keeps (MM-D27). The list is keyed as it parsed, so two
// renderings of one list share a menu.
func (r Request) Key() string {
	spec, _ := json.Marshal(r.Spec)
	list, _ := json.Marshal(r.List)
	program := ""
	if len(r.Program) > 0 {
		program = r.Program[len(r.Program)-1]
	}
	return cacheKey(spec, list, program)
}

// write builds the menu at into, the menu's absolute path, and returns the flag words naming it,
// nil when there is no menu to name, and whether this call wrote a new one (false for a reuse).
// It reuses the menu at into when the key file beside it names this request's key, and otherwise
// runs the program for its catalog, projects it, and writes the menu, then the key file. Every
// problem is a warning, and a menu this call could not build is removed, so no later build reuses
// a menu the inputs no longer describe.
func (r Request) write(into string, stderr io.Writer) (flag []string, wrote bool) {
	keyPath := into + ".key"
	if len(r.List) == 0 || len(r.Program) == 0 {
		// No list this launch: a menu left from an earlier one must not be reusable.
		removeMenu(into, keyPath)
		return nil, false
	}
	pre, bin := r.prefix(), r.Bin
	key := r.Key()
	if got, err := os.ReadFile(keyPath); err == nil && fileExists(into) {
		if gotKey, missing := parseKeyFile(got); gotKey == key {
			// A reused menu leaves out what the rebuilt one did, so it says so again: the
			// warning is about the menu this launch hands the program, not about the rebuild.
			warnMissing(stderr, pre, bin, missing)
			return Flag(r.Spec, into), false
		}
	}
	catalog, err := runCatalog(r.Program, r.Spec.Catalog, r.Env)
	if err != nil {
		fmt.Fprintf(stderr, "%scould not read %s's own model catalog (%v), so %s shows its own model menu.\n",
			pre, bin, err, bin)
		removeMenu(into, keyPath)
		return nil, false
	}
	res, err := Project(catalog, r.List, r.Spec)
	if err != nil {
		fmt.Fprintf(stderr, "%s%s's own model catalog is not the shape its pack declares (%v), so %s "+
			"shows its own model menu.\n", pre, bin, err, bin)
		removeMenu(into, keyPath)
		return nil, false
	}
	warnMissing(stderr, pre, bin, res.Missing)
	if res.Menu == nil {
		fmt.Fprintf(stderr, "%snone of yolo's models for this provider is in %s's own catalog, so %s "+
			"shows its own model menu.\n", pre, bin, bin)
		removeMenu(into, keyPath)
		return nil, false
	}
	if err := writeAtomic(into, res.Menu); err != nil {
		fmt.Fprintf(stderr, "%scould not write %s's model menu (%v), so %s shows its own.\n", pre, bin, err, bin)
		return nil, false
	}
	// The key is written last: a crash between the two leaves a menu with no key, which the next
	// launch rebuilds, never a key vouching for a menu that is not there. It carries the ids the
	// menu left out, so a launch that reuses the menu warns about them as this one did.
	_ = writeAtomic(keyPath, keyFile(key, res.Missing))
	return Flag(r.Spec, into), true
}

// Run is `yolo internal model-menu --bin=<bin> --spec=<json> -- <program> [prefix argv...]`: it
// reads yolo's list and, when the list names a model, the program's catalog (the program run with
// spec.Catalog appended, stdout captured), writes the menu to spec.Into and prints the flag words
// the launcher adds, each NUL-terminated, on stdout. It prints nothing, and the launcher adds
// nothing, whenever there is no menu to name, and it never fails the launch: every problem is a
// warning on stderr and exit 0, except a misuse, which is exit 2. home is the directory the
// spec's paths are relative to.
//
// The build is Request.write, the one `yolo host --` runs too (MM-D26); what is the jail's here
// is only where the list comes from, the file the boot rendered, and where the menu goes, the one
// path the pack declares, which every launch of the jail shares because they all read one list.
func Run(args []string, home string, stdout, stderr io.Writer) int {
	var bin, specJSON string
	var program []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			program = args[i+1:]
			i = len(args)
		case strings.HasPrefix(a, "--bin="):
			bin = strings.TrimPrefix(a, "--bin=")
		case strings.HasPrefix(a, "--spec="):
			specJSON = strings.TrimPrefix(a, "--spec=")
		default:
			fmt.Fprintf(stderr, "yolo internal %s: unexpected argument %q\n", Verb, a)
			return 2
		}
	}
	var spec packdecl.ModelMenu
	if bin == "" || len(program) == 0 || json.Unmarshal([]byte(specJSON), &spec) != nil ||
		spec.List == "" || spec.Into == "" || spec.Entries == "" || spec.ID == "" || home == "" {
		fmt.Fprintf(stderr, "usage: yolo internal %s --bin=<bin> --spec=<json> -- <program> [argv...]\n", Verb)
		return 2
	}
	req := Request{Bin: bin, Spec: spec, Program: program,
		List: ReadList(filepath.Join(home, filepath.FromSlash(spec.List)))}
	flag, _ := req.write(filepath.Join(home, filepath.FromSlash(spec.Into)), stderr)
	printFlag(stdout, flag)
	return 0
}

// warnMissing says which of yolo's listed ids the program's own catalog lacks, and so the menu
// leaves out, or nothing when there are none. Printed at EVERY launch whose menu lacks them, the
// one that rebuilt it and each that reuses it (MM-D9: "left out, with a warning").
func warnMissing(stderr io.Writer, prefix, bin string, missing []string) {
	if len(missing) == 0 {
		return
	}
	these, it := "the model", "it"
	if len(missing) > 1 {
		these, it = "these models", "them"
	}
	fmt.Fprintf(stderr, "%s%s's own model catalog has no %s, so its model menu leaves %s out. "+
		"A %s older than %s lacks %s; updating %s adds %s.\n",
		prefix, bin, strings.Join(missing, ", "), it, bin, these, it, bin, it)
}

// keyFile is the key file's bytes: the cache key on the first line, then the listed ids the menu
// left out, as a JSON array, so no id can be split or lost on its way back.
func keyFile(key string, missing []string) []byte {
	ids, _ := json.Marshal(append([]string{}, missing...))
	return []byte(key + "\n" + string(ids))
}

// parseKeyFile is keyFile's inverse: the key, and the ids the menu left out. A second line that
// is not a JSON array of strings reads as none left out.
func parseKeyFile(data []byte) (key string, missing []string) {
	key, rest, _ := strings.Cut(string(data), "\n")
	_ = json.Unmarshal([]byte(rest), &missing)
	return key, missing
}

// cacheKey names what the menu was built from: the declaration, the list, and the program file's
// identity (resolved, so an update that repoints a symlink changes it). A menu whose key matches
// is reused without running the program.
func cacheKey(spec, list []byte, program string) string {
	h := sha256.New()
	fmt.Fprintf(h, "spec\x00%s\x00", spec)
	h.Write(list)
	h.Write([]byte{0})
	if resolved, err := filepath.EvalSymlinks(program); err == nil {
		program = resolved
	}
	if fi, err := os.Stat(program); err == nil {
		fmt.Fprintf(h, "%s\x00%d\x00%d", program, fi.Size(), fi.ModTime().UnixNano())
	} else {
		fmt.Fprintf(h, "%s\x00absent", program)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// runCatalog runs program with argv appended, bounded in time and output, and returns its
// stdout. Its stdin is empty and its stderr discarded: the catalog is all it is asked for. env is
// its environment, nil for this process's own.
func runCatalog(program, argv, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program[0], append(append([]string(nil), program[1:]...), argv...)...)
	cmd.Env = env
	var out limitedBuffer
	out.limit = maxCatalogBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("it did not finish in %s", catalogTimeout)
		}
		return nil, fmt.Errorf("%s %s: %v", filepath.Base(program[len(program)-1]), strings.Join(argv, " "), err)
	}
	if out.over {
		return nil, fmt.Errorf("it printed more than %d bytes", maxCatalogBytes)
	}
	return out.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func printFlag(w io.Writer, words []string) {
	for _, word := range words {
		fmt.Fprintf(w, "%s\x00", word)
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func removeMenu(into, keyPath string) {
	_ = os.Remove(keyPath)
	_ = os.Remove(into)
}

// writeAtomic writes data to path through a temporary file in its directory, 0600, so a program
// reading the menu never sees half of one.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
