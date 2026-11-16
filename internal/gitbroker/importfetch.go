package gitbroker

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	runtimeidentity "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const (
	ImportAccessAuthenticated = "authenticated"
	ImportAccessAnonymous     = "anonymous"
)

// ImportFetchRequest is the host-side caller's one fetch of a skill import. The two authority
// snapshots are intersected here before a temporary broker scope is written; neither the Git
// URL nor the object store can add a remote or a read grant.
type ImportFetchRequest struct {
	Workspace       string
	URL             string
	Revision        string
	Ref             string
	StoreDir        string
	ApprovedRemotes []string
	CurrentRemotes  []string
	ApprovedGrants  []brokerscope.Grant
	CurrentGrants   []brokerscope.Grant

	// The fields below are test seams. Production callers use the installed yolo executable,
	// the host trust roots and the normal host Git/SSH binaries.
	helperCommand []string
	helperEnv     []string
	rootCAs       *x509.CertPool
}

// ImportFetchResult identifies the fetched object and the authority class under which the
// broker actually transferred it.
type ImportFetchResult struct {
	Commit      string
	ResolvedRef string
	AccessClass string
}

// FetchImport transfers one ref or full object id through the existing Git broker's fetch
// operation, then adds the resulting commit objects to the caller-owned bare store. The Git
// client is isolated from workspace and ambient Git configuration; its only remote is the
// existing yolo remote helper connected to a short-lived instance of this broker.
func FetchImport(ctx context.Context, request ImportFetchRequest) (ImportFetchResult, error) {
	var result ImportFetchResult
	if request.Workspace == "" || request.StoreDir == "" {
		return result, errors.New("workspace and object store are required")
	}
	if request.Revision != "" && !validObjectID(request.Revision) {
		return result, errors.New("revision must be a full hexadecimal Git object id")
	}
	transport, err := transportOf(request.URL)
	if err != nil {
		return result, fmt.Errorf("git broker refused import URL: %w", err)
	}
	if err := rejectURLCredentials(request.URL); err != nil {
		return result, err
	}
	if _, allowed, err := DecideImportAccess(request.URL, request.ApprovedRemotes, request.CurrentRemotes,
		request.ApprovedGrants, request.CurrentGrants); err != nil || !allowed {
		if err != nil {
			return result, err
		}
		return result, errors.New("git broker refused this import URL")
	}
	workspace := runtimeidentity.ResolveWorkspace(request.Workspace)
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		return result, fmt.Errorf("git executable is unavailable: %w", err)
	}
	if why := brokercore.PlacementRefusal("git", workspace, workspace)(gitExecutable); why != "" {
		return result, errors.New(why)
	}
	storeDir, err := filepath.Abs(request.StoreDir)
	if err != nil {
		return result, err
	}
	if withinPath(workspace, storeDir) {
		return result, errors.New("host import object store must be outside the workspace")
	}

	global := paths.GlobalStorage()
	if err := os.MkdirAll(global, 0o700); err != nil {
		return result, fmt.Errorf("create yolo host storage for the brokered fetch: %w", err)
	}
	workDir, err := os.MkdirTemp(global, "git-import-fetch-")
	if err != nil {
		return result, fmt.Errorf("create temporary brokered-fetch work area: %w", err)
	}
	defer os.RemoveAll(workDir)

	remotes := intersectStrings(request.ApprovedRemotes, request.CurrentRemotes)
	grants := intersectReadGrants(request.ApprovedGrants, request.CurrentGrants)
	launchID, err := brokerscope.NewLaunchID()
	if err != nil {
		return result, err
	}
	scope := brokerscope.File{
		Source: Source, LaunchID: launchID, PID: os.Getpid(),
		Container: runtimeidentity.FromWorkspace(workspace), Workspace: workspace,
		Form: brokerscope.FormRemoteURL, Repos: remotes, Grants: grants,
	}
	scopePath, err := brokerscope.Write(scope)
	if err != nil {
		return result, fmt.Errorf("write temporary Git broker scope: %w", err)
	}
	defer func() { _ = brokerscope.Remove(scopePath) }()

	brokerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	broker, cleanup, err := newBroker(brokerCtx, scopePath, scope, workspace, io.Discard, os.Getenv)
	if err != nil {
		return result, fmt.Errorf("start temporary Git broker: %w", err)
	}
	defer cleanup()
	broker.trackImportAccess = true
	broker.rootCAs = request.rootCAs

	endpointDir := filepath.Join(workDir, "endpoint")
	if err := os.Mkdir(endpointDir, 0o700); err != nil {
		return result, err
	}
	endpoint := filepath.Join(endpointDir, "git-broker.endpoint")
	stop := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() { serveDone <- hostservice.ServeLocalEndpoint(broker.Handle, endpoint, stop) }()
	if err := waitForEndpoint(ctx, endpoint, serveDone); err != nil {
		close(stop)
		<-serveDone
		return result, fmt.Errorf("start local Git broker endpoint: %w", err)
	}
	defer func() {
		close(stop)
		<-serveDone
	}()

	helperDir := filepath.Join(workDir, "helper-bin")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		return result, err
	}
	prefix := request.helperCommand
	if len(prefix) == 0 {
		executable, err := os.Executable()
		if err != nil {
			return result, fmt.Errorf("locate yolo for the Git broker helper: %w", err)
		}
		prefix = []string{executable, "git-remote", "--"}
	}
	body := "#!/bin/sh\nexec " + shquote.Join(prefix) + " \"$@\"\n"
	helperPath := filepath.Join(helperDir, "git-remote-yolo")
	if err := os.WriteFile(helperPath, []byte(body), 0o700); err != nil {
		return result, fmt.Errorf("write temporary Git broker helper: %w", err)
	}

	scratch := filepath.Join(workDir, "objects.git")
	gitEnv := importGitEnv(helperDir, endpoint, request.helperEnv)
	objectFormat := "sha1"
	if len(request.Revision) == 64 {
		objectFormat = "sha256"
	}
	if err := initImportRepo(ctx, gitEnv, scratch, objectFormat); err != nil {
		return result, fmt.Errorf("initialize temporary import object store: %w", err)
	}
	remote := "yolo::" + request.URL
	resolvedRef := ""
	commit := request.Revision
	if commit == "" {
		refs, defaultRef, err := listImportRefs(ctx, scratch, remote, gitEnv)
		if err != nil {
			return result, err
		}
		resolvedRef, commit, err = selectImportRef(request.Ref, refs, defaultRef)
		if err != nil {
			return result, err
		}
		if len(commit) == 64 && objectFormat != "sha256" {
			if err := os.RemoveAll(scratch); err != nil {
				return result, err
			}
			if err := initImportRepo(ctx, gitEnv, scratch, "sha256"); err != nil {
				return result, fmt.Errorf("initialize SHA-256 import object store: %w", err)
			}
			objectFormat = "sha256"
		}
		if _, err := runImportGit(ctx, gitEnv, "-C", scratch, "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head",
			remote, "+"+resolvedRef+":refs/yolo-import/selected"); err != nil {
			return result, fmt.Errorf("fetch selected skill ref through the Git broker: %w", err)
		}
		commit, err = runImportGit(ctx, gitEnv, "-C", scratch, "rev-parse", "--verify", "refs/yolo-import/selected^{commit}")
		if err != nil {
			return result, fmt.Errorf("selected skill ref does not name a commit: %w", err)
		}
	} else {
		if _, err := runImportGit(ctx, gitEnv, "-C", scratch, "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", remote, commit); err != nil {
			return result, fmt.Errorf("fetch pinned skill commit through the Git broker: %w", err)
		}
		commit, err = runImportGit(ctx, gitEnv, "-C", scratch, "rev-parse", "--verify", commit+"^{commit}")
		if err != nil {
			return result, fmt.Errorf("pinned skill revision is not a commit: %w", err)
		}
	}
	if !validObjectID(commit) {
		return result, fmt.Errorf("git returned an invalid commit object id %q", commit)
	}
	if err := addImportObjects(ctx, gitEnv, scratch, storeDir, commit); err != nil {
		return result, fmt.Errorf("add broker-fetched objects to the import store: %w", err)
	}
	accessClass := ImportAccessAuthenticated
	if transport == transportHTTPS && !broker.importUsedCredential.Load() {
		accessClass = ImportAccessAnonymous
	}
	return ImportFetchResult{Commit: commit, ResolvedRef: resolvedRef, AccessClass: accessClass}, nil
}

func rejectURLCredentials(remote string) error {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return fmt.Errorf("invalid import URL: %w", err)
		}
		if u.User != nil {
			return errors.New("git import URLs may not carry credentials")
		}
	}
	return nil
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func withinPath(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func intersectStrings(approved, current []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range approved {
		for _, c := range current {
			if a == c && !seen[a] {
				out = append(out, a)
				seen[a] = true
				break
			}
		}
	}
	return out
}

func intersectReadGrants(approved, current []brokerscope.Grant) []brokerscope.Grant {
	var out []brokerscope.Grant
	for _, a := range approved {
		if a.Kind != "read" || a.Repo != brokerscope.AnyRepo || a.RepoPattern || a.Branch != "" || a.Tag != "" || a.Workflow != "" || a.Actor != "" {
			continue
		}
		for _, c := range current {
			if c == a {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

type importRef struct {
	name string
	sha  string
}

func listImportRefs(ctx context.Context, scratch, remote string, env []string) ([]importRef, string, error) {
	output, err := runImportGit(ctx, env, "-C", scratch, "ls-remote", "--symref", remote)
	if err != nil {
		return nil, "", fmt.Errorf("list skill repository refs through the Git broker: %w", err)
	}
	return parseImportRefs(output)
}

func parseImportRefs(output string) ([]importRef, string, error) {
	var refs []importRef
	defaultRef := ""
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ref: ") {
			fields := strings.Fields(strings.TrimPrefix(line, "ref: "))
			if len(fields) != 2 || fields[1] != "HEAD" || !strings.HasPrefix(fields[0], "refs/heads/") || defaultRef != "" {
				return nil, "", fmt.Errorf("git broker returned a malformed default-branch advertisement %q", line)
			}
			defaultRef = fields[0]
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !validObjectID(fields[0]) || fields[1] == "" {
			return nil, "", fmt.Errorf("git broker returned a malformed ref advertisement %q", line)
		}
		if fields[1] == "HEAD" {
			continue
		}
		refs = append(refs, importRef{name: fields[1], sha: fields[0]})
	}
	return refs, defaultRef, nil
}

func selectImportRef(requested string, refs []importRef, defaultRef string) (string, string, error) {
	if requested == "" {
		if !strings.HasPrefix(defaultRef, "refs/heads/") {
			return "", "", errors.New("the remote did not advertise a default branch; pass --ref <branch> and try again")
		}
		requested = defaultRef
	}
	if strings.HasPrefix(requested, "refs/") {
		for _, ref := range refs {
			if ref.name == requested {
				return ref.name, ref.sha, nil
			}
		}
		return "", "", fmt.Errorf("skill ref %q does not exist in the remote advertisement", requested)
	}
	branch, tag := "refs/heads/"+requested, "refs/tags/"+requested
	var found *importRef
	for _, ref := range refs {
		if ref.name != branch && ref.name != tag {
			continue
		}
		if found != nil {
			return "", "", fmt.Errorf("skill ref %q matches both a branch and a tag; use its full refs/heads/ or refs/tags/ name", requested)
		}
		copy := ref
		found = &copy
	}
	if found == nil {
		return "", "", fmt.Errorf("skill ref %q was not advertised by the remote", requested)
	}
	return found.name, found.sha, nil
}

func addImportObjects(ctx context.Context, env []string, source, destination, commit string) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := initImportRepo(ctx, env, destination, objectFormatOf(commit)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("import object store is not a real directory")
	}
	if _, err := runImportGit(ctx, env, "-C", destination, "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", source, commit); err != nil {
		return err
	}
	return nil
}

func objectFormatOf(objectID string) string {
	if len(objectID) == 64 {
		return "sha256"
	}
	return "sha1"
}

func initImportRepo(ctx context.Context, env []string, path, objectFormat string) error {
	args := []string{"init", "--bare", "--template="}
	if objectFormat == "sha256" {
		args = append(args, "--object-format=sha256")
	}
	args = append(args, path)
	_, err := runImportGit(ctx, env, args...)
	return err
}

func importGitEnv(helperDir, endpoint string, extra []string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, paths.ServiceEnvVarPrefix) || key == "GO_HELPER" {
			continue
		}
		env = append(env, item)
	}
	path := helperDir + string(os.PathListSeparator) + os.Getenv("PATH")
	endpointEnv := paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug(Name) + paths.ServiceEnvVarSuffix
	env = append(env,
		"PATH="+path,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=yolo:file",
		endpointEnv+"="+endpoint,
	)
	return append(env, extra...)
}

func runImportGit(ctx context.Context, env []string, args ...string) (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git executable is unavailable: %w", err)
	}
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func waitForEndpoint(ctx context.Context, endpoint string, done <-chan error) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	guard := time.NewTimer(30 * time.Second)
	defer guard.Stop()
	for {
		if _, err := os.Stat(endpoint); err == nil {
			return nil
		}
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			return errors.New("endpoint stopped before it was published")
		case <-ctx.Done():
			return ctx.Err()
		case <-guard.C:
			return errors.New("endpoint was not published before the startup guard expired")
		case <-ticker.C:
		}
	}
}
