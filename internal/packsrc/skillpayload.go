package packsrc

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nofollow"
)

const skillPayloadFormat = "1"
const skillEntryLimit = 32768
const skillByteLimit int64 = 256 << 20
const skillListingLimit = 16 << 20
const skillMarkerLimit = 1 << 20
const skillLinkLimit = 4096

// SkillState is a derived caller-local state, not stored in the shared record.
type SkillState string

const (
	SkillReady           SkillState = "READY"
	SkillRefused         SkillState = "REFUSED"
	SkillNotFetched      SkillState = "NOT FETCHED"
	SkillNotMaterialized SkillState = "NOT MATERIALIZED"
)

// SkillRequest is one immutable selected declaration, before any audience gating.
type SkillRequest struct{ Label, Source, License string }

// SkillResolveOptions controls the grouped writer. Readers take only a record path.
type SkillResolveOptions struct {
	LockPath string
	Force    bool
	Now      func() time.Time
	Waiting  func(string)
}

// SkillOmission records a whole-root omission; counts include omitted entries.
type SkillOmission struct{ Path, Reason string }

// SkillEntry is an object-validated entry. Data is not a checkout or a declared path.
type SkillEntry struct {
	Path, Mode, OID, Target string
	Size                    int64
	Data                    []byte
}

// SkillRowPlan is the validated selected registration content, before notch mapping.
type SkillRowPlan struct {
	Request     SkillRequest
	Path        string
	Marker      []byte
	Entries     []string
	LicenseName string
	License     []byte
	LicenseNote string
	Executables int
}

// SkillPayloadPlan is the common pure object validation result used by both readers and writers.
type SkillPayloadPlan struct {
	Entries            []SkillEntry
	Omissions          []SkillOmission
	Rows               []SkillRowPlan
	ChargedEntries     int
	ChargedBytes       int64
	Files, Executables int
}

// SkillPayloadResult is ONE immutable result per repository/ref group for this invocation.
type SkillPayloadResult struct {
	Repo, Ref, Commit, Payload string
	State                      SkillState
	Reason, Warning            string
	Fetched                    bool
	Plan                       *SkillPayloadPlan
}

func (r SkillPayloadResult) Diagnostic(row string) string {
	if r.State == SkillNotMaterialized {
		return row + ": not materialized — objects present and valid; published payload absent; the next fresh launch or host writer rematerializes from local objects"
	}
	if r.State == SkillReady {
		return row + ": ready at " + r.Commit
	}
	if r.State == SkillNotFetched {
		return row + ": not fetched — " + r.Reason + "; run yolo pack install"
	}
	return row + ": refused at recorded commit " + r.Commit + " — " + r.Reason + "; every selection naming this repository/ref shares the record; pin source to a usable full commit or correct/remove the row (yolo pack update helps only if upstream moved)"
}

// SkillPayloadPath is outside pack trees and agent discovery roots, shared across refs/aliases.
func (s *Store) SkillPayloadPath(repo, commit string) string {
	sum := sha256.Sum256([]byte(repo + "\n" + commit + "\n" + skillPayloadFormat))
	return filepath.Join(s.Dir, "skill-payloads", hex.EncodeToString(sum[:]))
}

type skillGroup struct {
	addr Addr
	rows []SkillRequest
}

func groupSkillRequests(rows []SkillRequest) ([]skillGroup, error) {
	var groups []skillGroup
	index := map[string]int{}
	for _, r := range rows {
		a, err := Parse(r.Source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Label, err)
		}
		if a.Kind != KindGit || a.Path == "" {
			return nil, fmt.Errorf("%s: source needs a non-root Git skill directory — correct the skills row", r.Label)
		}
		key := skillPair(a.Repo, a.Ref)
		if i, ok := index[key]; ok {
			groups[i].rows = append(groups[i].rows, r)
		} else {
			index[key] = len(groups)
			groups = append(groups, skillGroup{addr: a, rows: []SkillRequest{r}})
		}
	}
	return groups, nil
}

// ReadSkillPayloads reads one record snapshot, with no lock, fetch, stamp or write.
// A complete valid object tree but absent published payload is NOT MATERIALIZED.
func (s *Store) ReadSkillPayloads(rows []SkillRequest, lockPath string) []SkillPayloadResult {
	groups, err := groupSkillRequests(rows)
	if err != nil {
		return []SkillPayloadResult{{State: SkillRefused, Reason: err.Error()}}
	}
	l, err := loadSkillsLock(lockPath)
	var results []SkillPayloadResult
	for _, g := range groups {
		r := SkillPayloadResult{Repo: g.addr.Repo, Ref: g.addr.Ref, State: SkillNotFetched, Reason: "no shared record"}
		if err != nil {
			r.State = SkillRefused
			r.Reason = err.Error()
		} else if record, ok := l.Pairs[skillPair(r.Repo, r.Ref)]; ok {
			r = s.inspectSkillGroup(g, record.Commit)
		}
		results = append(results, r)
	}
	return results
}

// ResolveSkillPayloads snapshots all requests, groups across declaring packs, and
// serializes each entire group under ONE mirror lock. It never calls checkout Refresh.
func (s *Store) ResolveSkillPayloads(rows []SkillRequest, opts SkillResolveOptions) []SkillPayloadResult {
	rows = append([]SkillRequest(nil), rows...)
	groups, err := groupSkillRequests(rows)
	if err != nil {
		return []SkillPayloadResult{{State: SkillRefused, Reason: err.Error()}}
	}
	var out []SkillPayloadResult
	for _, g := range groups {
		out = append(out, s.resolveSkillGroup(g, opts))
	}
	return out
}

func (s *Store) resolveSkillGroup(g skillGroup, opts SkillResolveOptions) SkillPayloadResult {
	r := SkillPayloadResult{Repo: g.addr.Repo, Ref: g.addr.Ref, State: SkillNotFetched, Reason: "no shared record"}
	unlock, err := s.lockMirror(r.Repo, opts.Waiting)
	if err != nil {
		r.Reason = err.Error()
		return r
	}
	defer unlock()
	l, err := loadSkillsLock(opts.LockPath)
	if err != nil {
		r.State = SkillRefused
		r.Reason = err.Error()
		return r
	}
	record, recorded := l.Pairs[skillPair(r.Repo, r.Ref)]
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	eligible := !recorded || opts.Force
	mirror := s.mirrorPath(r.Repo)
	if recorded && !opts.Force && !isFullSHA(r.Ref) {
		kind, _, _, e := s.classifyRef(mirror, r.Ref)
		if e != nil {
			r.Warning = "could not classify ref: " + e.Error()
		} else if kind == refBranch || kind == refUnresolved && record.Kind == "branch" {
			eligible = !s.stampFresh(g.addr, now, BranchRefreshInterval)
		}
	}
	var candidate string
	var candidateErr error
	if eligible {
		b, cancel := s.newBudget()
		defer cancel()
		// Eligibility is already settled. fetchStep's always arm supplies transport,
		// fsck, pair stamps and tag restoration, not its extra missing-local-ref trigger.
		step := s.fetchStep(b, r.Repo, []Addr{g.addr}, fetchMode{always: true, keepTags: !opts.Force}, now, BranchRefreshInterval)
		r.Fetched = step.fetched
		candidateErr = step.fetchErr
		if candidateErr == nil {
			candidate, candidateErr = s.revParse(mirror, r.Ref)
			if candidateErr == nil {
				candidateErr = s.acquireSkillBlobs(b, mirror, candidate)
			}
		}
	}
	if candidate != "" && candidateErr == nil {
		c := s.inspectSkillGroup(g, candidate)
		if c.State == SkillReady || c.State == SkillNotMaterialized {
			_, payloadErr := os.Lstat(c.Payload)
			created := os.IsNotExist(payloadErr)
			if err = s.publishSkillPayload(c.Payload, c.Plan); err == nil {
				kind, _, _, kindErr := s.classifyRef(mirror, r.Ref)
				if kindErr != nil {
					err = kindErr
				} else {
					err = s.saveSkillRecord(opts.LockPath, SkillRecord{Repo: r.Repo, Ref: r.Ref, Commit: candidate, Kind: skillRefKind(kind)}, opts.Waiting)
				}
			}
			if err == nil {
				c.State = SkillReady
				c.Fetched = r.Fetched
				return c
			}
			if created {
				removeSkillPayload(c.Payload)
			}
			candidateErr = err
		} else {
			candidateErr = errors.New(c.Reason)
		}
	}
	if recorded {
		fallback := s.inspectSkillGroup(g, record.Commit)
		fallback.Fetched = r.Fetched
		if fallback.State == SkillNotMaterialized {
			if err = s.publishSkillPayload(fallback.Payload, fallback.Plan); err != nil {
				fallback.State = SkillRefused
				fallback.Reason = err.Error()
			} else {
				fallback.State = SkillReady
			}
		}
		if candidateErr != nil {
			fallback.Warning = fmt.Sprintf("could not use candidate %s (%s) — recorded %s revalidated for this entire selected group", candidate, candidateErr, record.Commit)
		}
		if r.Warning != "" {
			fallback.Warning = r.Warning
		}
		return fallback
	}
	if candidateErr != nil {
		r.Reason = candidateErr.Error()
	}
	if candidate != "" {
		r.Commit = candidate
		r.Payload = s.SkillPayloadPath(r.Repo, candidate)
		r.State = SkillRefused
	}
	return r
}

var errSkillObjectsMissing = errors.New("sourced skill objects absent")

func (s *Store) inspectSkillGroup(g skillGroup, commit string) SkillPayloadResult {
	r := SkillPayloadResult{Repo: g.addr.Repo, Ref: g.addr.Ref, Commit: commit, Payload: s.SkillPayloadPath(g.addr.Repo, commit)}
	plan, err := s.planSkillPayload(s.mirrorPath(g.addr.Repo), commit, g.rows)
	if err != nil {
		r.State = SkillRefused
		r.Reason = err.Error()
		if errors.Is(err, errSkillObjectsMissing) {
			r.State = SkillNotFetched
		}
		return r
	}
	r.Plan = plan
	fi, err := os.Lstat(r.Payload)
	if os.IsNotExist(err) {
		r.State = SkillNotMaterialized
		return r
	}
	if err != nil || !fi.IsDir() {
		r.State = SkillRefused
		r.Reason = "published payload is not a regular directory — remove the damaged payload and run yolo pack install"
		return r
	}
	if err := s.verifySkillPayload(r.Payload, plan); err != nil {
		r.State = SkillRefused
		r.Reason = fmt.Sprintf("published payload invalid: %v — remove the damaged payload and run yolo pack install", err)
		return r
	}
	r.State = SkillReady
	return r
}

// limitedOutput bounds stdout while the subprocess streams; a bound is not an
// allocation after an unbounded Output call. Git's usual deadline/cancellation applies.
func (s *Store) skillObjectRead(mirror string, limit int, args ...string) ([]byte, error) {
	b, cancel := s.newBudget()
	defer cancel()
	cmd := s.gitCmd(b.ctx, mirror, args...)
	isolateSkillGit(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", errSkillObjectsMissing, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	if len(data) > limit || readErr != nil {
		cancel()
	}
	err = cmd.Wait()
	if len(data) > limit {
		return nil, fmt.Errorf("object output %d exceeds bound %d — choose a smaller repository", len(data), limit)
	}
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: git %s: %s", errSkillObjectsMissing, args[0], strings.TrimSpace(stderr.String()))
	}
	return data, nil
}

func (s *Store) planSkillPayload(mirror, commit string, rows []SkillRequest) (*SkillPayloadPlan, error) {
	listing, err := s.skillObjectRead(mirror, skillListingLimit, "ls-tree", "-r", "-t", "-l", "-z", commit)
	if err != nil {
		return nil, err
	}
	p := &SkillPayloadPlan{}
	entries := map[string]*SkillEntry{}
	for _, rec := range bytes.Split(listing, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(rec), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 || !skillCleanPath(name) {
			return nil, fmt.Errorf("invalid Git tree entry %q — correct the repository tree", rec)
		}
		e := SkillEntry{Path: name, Mode: fields[0], OID: fields[2]}
		if fields[3] != "-" {
			e.Size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: blob size for %s", errSkillObjectsMissing, name)
			}
		}
		p.ChargedEntries++
		p.ChargedBytes += e.Size
		if p.ChargedEntries > skillEntryLimit {
			return nil, fmt.Errorf("payload entries %d exceed bound %d — choose a smaller repository", p.ChargedEntries, skillEntryLimit)
		}
		if p.ChargedBytes > skillByteLimit {
			return nil, fmt.Errorf("payload bytes %d exceed bound %d — choose a smaller repository", p.ChargedBytes, skillByteLimit)
		}
		p.Entries = append(p.Entries, e)
	}
	for i := range p.Entries {
		e := &p.Entries[i]
		entries[e.Path] = e
	}
	// Charge every blob before reading any, including entries that will be omitted.
	if err := s.readSkillBlobs(mirror, p.Entries); err != nil {
		return nil, err
	}
	for i := range p.Entries {
		e := &p.Entries[i]
		if e.Mode == "120000" {
			e.Target = string(e.Data)
			if len(e.Target) > skillLinkLimit {
				return nil, fmt.Errorf("link target %s has %d bytes, bound %d — shorten it", e.Path, len(e.Target), skillLinkLimit)
			}
		}
	}
	omitted := map[string]string{}
	caseGroups := map[string][]string{}
	for _, e := range p.Entries {
		key := path.Dir(e.Path) + "/" + strings.ToLower(path.Base(e.Path))
		caseGroups[key] = append(caseGroups[key], e.Path)
	}
	for _, group := range caseGroups {
		if len(group) > 1 {
			sort.Strings(group)
			for _, name := range group {
				omitted[name] = "case-collision group {" + strings.Join(group, ", ") + "}"
			}
		}
	}
	for _, e := range p.Entries {
		switch e.Mode {
		case "040000", "100644", "100755":
		case "120000":
			if _, err := resolveSkillPath(entries, e.Path, "", true); err != nil {
				omitted[e.Path] = err.Error()
			}
		default:
			omitted[e.Path] = "unsupported mode " + e.Mode
		}
	}
	// Descendants of omitted directories and links to omitted targets are omitted
	// too. Never follow a payload link on disk or keep a case group for a selected row.
	for changed := true; changed; {
		changed = false
		for _, e := range p.Entries {
			if _, ok := omitted[e.Path]; ok {
				continue
			}
			for a := path.Dir(e.Path); a != "."; a = path.Dir(a) {
				if why, ok := omitted[a]; ok {
					omitted[e.Path] = "ancestor " + a + " omitted by " + why
					changed = true
					break
				}
			}
			if _, ok := omitted[e.Path]; ok {
				continue
			}
			if e.Mode == "120000" {
				resolved, _ := resolveSkillPath(entries, e.Path, "", true)
				if why, ok := omitted[resolved]; ok {
					omitted[e.Path] = "target " + resolved + " omitted by " + why
					changed = true
				}
			}
		}
	}
	for _, request := range rows {
		a, _ := Parse(request.Source)
		row, err := validateSkillRow(entries, omitted, request, a.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", request.Label, err)
		}
		p.Rows = append(p.Rows, row)
	}
	var kept []SkillEntry
	for _, e := range p.Entries {
		if reason, ok := omitted[e.Path]; ok {
			p.Omissions = append(p.Omissions, SkillOmission{Path: e.Path, Reason: reason})
			continue
		}
		kept = append(kept, e)
		if e.Mode == "100644" || e.Mode == "100755" {
			p.Files++
			if e.Mode == "100755" {
				p.Executables++
			}
		}
	}
	p.Entries = kept
	return p, nil
}

func skillCleanPath(p string) bool {
	return p != "" && p != "." && !strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n") && !strings.Contains("/"+p+"/", "/../")
}
func skillWithin(scope, p string) bool {
	return scope == "" || p == scope || strings.HasPrefix(p, scope+"/")
}

// resolveSkillPath walks Git entries, not the filesystem. Supporting cycles are
// kept; selected-directory cycles are refused. A link's target is bounded before use.
func resolveSkillPath(entries map[string]*SkillEntry, p, scope string, allowCycle bool) (string, error) {
	seen := map[string]bool{}
	for {
		if p == "." && scope == "" {
			return p, nil
		}
		if !skillWithin(scope, p) {
			return "", fmt.Errorf("link escapes selected directory %s", scope)
		}
		segs := strings.Split(p, "/")
		restart := false
		for i := range segs {
			prefix := strings.Join(segs[:i+1], "/")
			e := entries[prefix]
			if e == nil {
				return "", fmt.Errorf("dangling link path %s", prefix)
			}
			if e.Mode == "120000" {
				if len(e.Target) > skillLinkLimit {
					return "", fmt.Errorf("link target %s has %d bytes, bound %d — shorten it", prefix, len(e.Target), skillLinkLimit)
				}
				if strings.HasPrefix(e.Target, "/") || strings.ContainsAny(e.Target, "\\\x00\r\n") {
					return "", fmt.Errorf("absolute or invalid link %s", prefix)
				}
				if seen[prefix] {
					if allowCycle {
						return prefix, nil
					}
					return "", fmt.Errorf("cyclic link %s", prefix)
				}
				seen[prefix] = true
				target := path.Clean(path.Join(path.Dir(prefix), e.Target))
				if target == ".." || strings.HasPrefix(target, "../") {
					return "", fmt.Errorf("link %s escapes repository root", prefix)
				}
				if !skillWithin(scope, target) {
					return "", fmt.Errorf("link %s escapes selected directory %s", prefix, scope)
				}
				if i+1 < len(segs) {
					target = path.Join(target, strings.Join(segs[i+1:], "/"))
				}
				p = target
				restart = true
				break
			}
			if i+1 < len(segs) && e.Mode != "040000" {
				return "", fmt.Errorf("path crosses non-directory %s", prefix)
			}
		}
		if !restart {
			return p, nil
		}
	}
}

func validateSkillRow(entries map[string]*SkillEntry, omitted map[string]string, request SkillRequest, dir string) (SkillRowPlan, error) {
	row := SkillRowPlan{Request: request, Path: dir}
	for prefix := dir; prefix != "."; prefix = path.Dir(prefix) {
		if e := entries[prefix]; e == nil || e.Mode != "040000" {
			return row, fmt.Errorf("selected path %s absent or crosses a symlink/gitlink — select a real skill directory", prefix)
		}
	}
	for _, e := range entries {
		if !skillWithin(dir, e.Path) {
			continue
		}
		switch e.Mode {
		case "040000", "100644", "100755":
		case "120000":
			if _, err := resolveSkillPath(entries, e.Path, dir, false); err != nil {
				return row, err
			}
		default:
			return row, fmt.Errorf("selected path %s has unsupported mode %s", e.Path, e.Mode)
		}
		if why, ok := omitted[e.Path]; ok {
			return row, fmt.Errorf("selected path %s omitted by %s", e.Path, why)
		}
		if e.Mode == "100755" {
			row.Executables++
		}
		if path.Dir(e.Path) == dir && path.Base(e.Path) != "SKILL.md" {
			row.Entries = append(row.Entries, path.Base(e.Path))
		}
	}
	marker, err := resolveSkillPath(entries, dir+"/SKILL.md", dir, false)
	if err != nil {
		return row, fmt.Errorf("SKILL.md: %w", err)
	}
	e := entries[marker]
	if e.Mode != "100644" && e.Mode != "100755" {
		return row, fmt.Errorf("SKILL.md must resolve to a regular blob — correct %s", dir)
	}
	if e.Size > skillMarkerLimit {
		return row, fmt.Errorf("SKILL.md bytes %d exceed bound %d — shorten the marker", e.Size, skillMarkerLimit)
	}
	row.Marker = append([]byte(nil), e.Data...)
	licensePath := request.License
	names := []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"}
	if licensePath == "" {
		for _, n := range names {
			if entries[dir+"/"+n] != nil {
				return row, nil
			}
		}
		for _, n := range names {
			if e := entries[n]; e != nil && (e.Mode == "100644" || e.Mode == "100755") {
				licensePath = n
				break
			}
		}
	}
	if licensePath == "" {
		row.LicenseNote = "no license file found"
		sort.Strings(row.Entries)
		return row, nil
	}
	resolved, err := resolveSkillPath(entries, licensePath, "", false)
	if err != nil {
		return row, fmt.Errorf("license %s: %w", licensePath, err)
	}
	if why, ok := omitted[resolved]; ok {
		return row, fmt.Errorf("license %s omitted by %s", resolved, why)
	}
	e = entries[resolved]
	if e.Mode != "100644" && e.Mode != "100755" {
		return row, fmt.Errorf("license %s is not a regular blob — correct license", licensePath)
	}
	if e.Size > skillMarkerLimit {
		return row, fmt.Errorf("license bytes %d exceed bound %d — choose a smaller license file", e.Size, skillMarkerLimit)
	}
	row.LicenseName = path.Base(licensePath)
	for name := range entries {
		if path.Dir(name) == dir && strings.EqualFold(path.Base(name), row.LicenseName) {
			return row, fmt.Errorf("license basename %s conflicts with skill entry %s — choose another license path or remove the conflict", row.LicenseName, name)
		}
	}
	row.License = append([]byte(nil), e.Data...)
	sort.Strings(row.Entries)
	return row, nil
}

func (s *Store) publishSkillPayload(dest string, p *SkillPayloadPlan) error {
	if fi, err := os.Lstat(dest); err == nil {
		if fi.IsDir() {
			return s.verifySkillPayload(dest, p)
		}
		return fmt.Errorf("payload %s is not a directory — remove it and run yolo pack install", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(parent, ".payload-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = filepath.Walk(scratch, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
		_ = os.RemoveAll(scratch)
	}()
	for _, e := range p.Entries {
		full := filepath.Join(scratch, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		switch e.Mode {
		case "040000":
			err = os.MkdirAll(full, 0755)
		case "120000":
			err = os.Symlink(e.Target, full)
		default:
			mode := os.FileMode(0444)
			if e.Mode == "100755" {
				mode = 0555
			}
			err = os.WriteFile(full, e.Data, mode)
		}
		if err != nil {
			return err
		}
	}
	if err = filepath.Walk(scratch, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return os.Chmod(p, 0555)
		}
		return nil
	}); err != nil {
		return err
	}
	if err = s.parentCtx().Err(); err != nil {
		return err
	}
	if err = os.Rename(scratch, dest); err != nil {
		if fi, e := os.Lstat(dest); e == nil && fi.IsDir() {
			return s.verifySkillPayload(dest, p)
		}
		return err
	}
	return nil
}

func isolateSkillGit(cmd *exec.Cmd) {
	var env []string
	for _, v := range cmd.Env {
		if !strings.HasPrefix(v, "GIT_CONFIG_") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1")
}

// readSkillBlobs is one bounded batch, not one process per file. The listing's
// entire charged size was checked before this reads any bytes, omitted ones included.
func (s *Store) readSkillBlobs(mirror string, entries []SkillEntry) error {
	var input strings.Builder
	var requested []*SkillEntry
	for i := range entries {
		e := &entries[i]
		if e.Mode != "040000" && e.Mode != "160000" {
			input.WriteString(e.OID + "\n")
			requested = append(requested, e)
		}
	}
	if len(requested) == 0 {
		return nil
	}
	b, cancel := s.newBudget()
	defer cancel()
	cmd := s.gitCmd(b.ctx, mirror, "cat-file", "--batch")
	isolateSkillGit(cmd)
	cmd.Stdin = strings.NewReader(input.String())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", errSkillObjectsMissing, err)
	}
	r := bufio.NewReader(stdout)
	var readErr error
	for _, e := range requested {
		header, err := r.ReadString('\n')
		if err != nil {
			readErr = err
			break
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != e.OID || fields[1] != "blob" || fields[2] != strconv.FormatInt(e.Size, 10) {
			readErr = fmt.Errorf("%w: %s", errSkillObjectsMissing, strings.TrimSpace(header))
			break
		}
		e.Data = make([]byte, int(e.Size))
		if _, err = io.ReadFull(r, e.Data); err != nil {
			readErr = err
			break
		}
		if next, err := r.ReadByte(); err != nil || next != '\n' {
			readErr = fmt.Errorf("invalid blob batch for %s", e.Path)
			break
		}
	}
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return fmt.Errorf("%w: %v", errSkillObjectsMissing, readErr)
	}
	if waitErr != nil {
		return fmt.Errorf("%w: %v: %s", errSkillObjectsMissing, waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func skillRefKind(kind refKind) string {
	switch kind {
	case refBranch:
		return "branch"
	case refTag:
		return "tag"
	case refCommit:
		return "commit"
	default:
		return "other"
	}
}

func (s *Store) acquireSkillBlobs(b budget, mirror, commit string) error {
	listing, err := s.skillObjectRead(mirror, skillListingLimit, "rev-list", "--objects", "--missing=print", commit+"^{tree}")
	if err != nil {
		return err
	}
	var missing strings.Builder
	for _, line := range strings.Split(string(listing), "\n") {
		if oid, ok := strings.CutPrefix(line, "?"); ok {
			if !isFullSHA(oid) {
				return fmt.Errorf("invalid missing object %q — repair the pack mirror", oid)
			}
			missing.WriteString(oid + "\n")
		}
	}
	if missing.Len() == 0 {
		return nil
	}
	cmd := s.gitCmd(b.ctx, mirror, withFsck("-c", "fetch.negotiationAlgorithm=noop", "fetch", "origin", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", "--filter=blob:none", "--stdin")...)
	cmd.Stdin = strings.NewReader(missing.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not acquire every payload blob: %w: %s — run yolo pack install when the repository is reachable", err, strings.TrimSpace(stderr.String()))
	}
	// The common pure validator, not the fetch status, proves object completeness.
	return nil
}

func (s *Store) verifySkillPayload(root string, p *SkillPayloadPlan) error {
	expected := map[string]SkillEntry{}
	for _, e := range p.Entries {
		expected[e.Path] = e
	}
	seen := map[string]bool{}
	err := nofollow.WalkBelow(s.Dir, root, nil, func(g nofollow.Entry) error {
		rel := filepath.ToSlash(g.Rel)
		if rel == "." {
			if g.Kind != nofollow.Dir || g.Perm&0222 != 0 {
				return fmt.Errorf("payload root is not a sealed directory")
			}
			return nil
		}
		e, ok := expected[rel]
		if !ok {
			return fmt.Errorf("unplanned payload entry %s", rel)
		}
		seen[rel] = true
		switch e.Mode {
		case "040000":
			if g.Kind != nofollow.Dir || g.Perm&0222 != 0 {
				return fmt.Errorf("payload directory %s changed", rel)
			}
		case "120000":
			if g.Kind != nofollow.Symlink || g.Target != e.Target {
				return fmt.Errorf("payload link %s changed", rel)
			}
		default:
			want := os.FileMode(0444)
			if e.Mode == "100755" {
				want = 0555
			}
			if g.Kind != nofollow.File || g.Perm != want {
				return fmt.Errorf("payload file mode %s changed", rel)
			}
			got, err := io.ReadAll(io.LimitReader(g.File, e.Size+1))
			if err != nil {
				return err
			}
			if !bytes.Equal(got, e.Data) {
				return fmt.Errorf("payload file %s changed", rel)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("payload entries missing: got %d, expected %d", len(seen), len(expected))
	}
	return nil
}

func removeSkillPayload(root string) {
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(p, fi.Mode().Perm()|0700)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}
