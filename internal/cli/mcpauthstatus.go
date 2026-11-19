package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// readMCPStatusHost never uses the launch resolver. Only selected immutable
// metadata or an already-present staged tree can be read; absent packs stay absent.
func readMCPStatusHost(server string) (map[string]any, error) {
	cfg, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		return nil, errors.New("user configuration is unreadable; run yolo config check, then retry")
	}
	if cfg == nil {
		cfg = jsonx.NewOrderedMap()
	}
	entries, problems := config.ReadOnlyUserPackEntries(cfg)
	complete := len(problems) == 0
	originals := packload.EmbeddedFS()
	stagedRoot, _ := packload.EmbeddedLocation()
	readEmbedded := func(name string, only, exclude []string) (*packload.Pack, error) {
		if originals == nil {
			return nil, errors.New("selected metadata is not available without effect")
		}
		p, ps, e := packload.ReadSelectedPackFS(originals, name, filepath.Join("/embedded-originals", name), name, only, exclude)
		if e != nil || len(ps) > 0 {
			return nil, errors.New("selected metadata is not available without effect")
		}
		p.Official = true
		return p, nil
	}
	lookup := func(name string) (*packload.Pack, bool) { p, e := readEmbedded(name, nil, nil); return p, e == nil }
	selection, err := config.SelectPacks(entries, config.PackSelectSpec{EmbeddedLookup: lookup, Selection: hostLaunchSelection(cfg, "", ""), Resolve: func(entry config.PackEntry) (*packload.Pack, error) {
		if !entry.Embedded() {
			return nil, errors.New("selected custom pack is not determinable without effect")
		}
		p, e := readEmbedded(entry.EmbeddedName, entry.Only, entry.Exclude)
		if e != nil {
			return nil, e
		}
		p.Name = entry.Name
		p.Namespace = entry.Namespace
		p.NamespaceOwner = entry.NamespaceOwner
		return p, nil
	}})
	if err != nil || !selection.Complete() {
		complete = false
	}
	packs := selection.Packs()
	// Already staged sources remain on disk. Originals alone never become a point.
	staged := []*packload.Pack{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		root := filepath.Join(stagedRoot, p.Name)
		info, statErr := os.Lstat(root)
		if stagedRoot != "" && statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			q, ps := packload.LoadDirForUse(root, p.Name)
			if q != nil && len(ps) == 0 && reflect.DeepEqual(q.Decl, p.Decl) {
				q.Official = p.Official
				staged = append(staged, q)
				continue
			}
		}
		staged = append(staged, p)
	}
	share, err := run.HostHeaderShare(cfg, staged, paths.Home(), paths.Home())
	if err != nil {
		return nil, errors.New("user MCP composition is unreadable; run yolo config check, then retry")
	}
	if share.SignIn != nil && !complete {
		share.SignIn.Advice.MappingReadable = false
	}
	rows := mcpauthdaemon.ReadHostStatus(loopholes.StateDirFor("mcp-auth-broker"), share, server)
	for _, row := range rows {
		if row["state"] == "retained, unused" {
			continue
		}
		points, _ := row["points"].([]any)
		if len(points) == 0 {
			row["points"] = []any{map[string]any{"state": "not determinable without effect", "next_step": "launch once, or run yolo mcp-auth status inside a jail"}}
		}
	}
	return map[string]any{"schema": "yolo.mcp-auth.status/1", "agents": []any{}, "once": rows}, nil
}

var mcpAuthHostStatus = readMCPStatusHost

func mcpAuthStatusMain(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	jsonForm := false
	server := ""
	for _, arg := range args {
		if arg == "--json" && !jsonForm {
			jsonForm = true
		} else if server == "" && !strings.HasPrefix(arg, "-") {
			server = arg
		} else {
			fmt.Fprintln(stderr, "yolo mcp-auth: expected `status [SERVER] [--json]`; see yolo mcp-auth --help")
			return 2
		}
	}
	var body map[string]any
	if config.InJail() {
		endpoint := getenv(mcpAuthEndpointEnv)
		if endpoint == "" {
			fmt.Fprintln(stderr, "yolo mcp-auth: broker unreachable; run yolo mcp-auth status on the host, then relaunch")
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		conn, err := mcpAuthDial(endpoint, time.Until(deadline))
		if err != nil {
			fmt.Fprintln(stderr, "yolo mcp-auth: broker unreachable; run yolo mcp-auth status on the host, then relaunch")
			return 1
		}
		defer conn.Close()
		_ = conn.SetDeadline(deadline)
		request, _ := json.Marshal(map[string]string{"op": "status", "server": server})
		if frameproto.WriteRequest(conn, request) != nil {
			fmt.Fprintln(stderr, "yolo mcp-auth: status request unavailable; retry or run yolo mcp-auth status on the host")
			return 1
		}
		var bytes []byte
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				fmt.Fprintln(stderr, "yolo mcp-auth: status answer unavailable; retry or run yolo mcp-auth status on the host")
				return 1
			}
			if frame.StreamID == frameproto.StreamStdout {
				bytes = append(bytes, frame.Payload...)
			}
			if frame.StreamID == frameproto.StreamExit {
				rc, err := frameproto.ExitCode(frame.Payload)
				if err != nil || rc != 0 {
					fmt.Fprintln(stderr, "yolo mcp-auth: status unavailable; run yolo mcp-auth status on the host, then relaunch")
					return 1
				}
				if json.Unmarshal(bytes, &body) != nil {
					fmt.Fprintln(stderr, "yolo mcp-auth: status answer unreadable; relaunch with a current yolo build")
					return 1
				}
				break
			}
		}
	} else {
		var err error
		body, err = mcpAuthHostStatus(server)
		if err != nil {
			fmt.Fprintln(stderr, "yolo mcp-auth: "+err.Error())
			return 1
		}
	}
	rows, err := mcpStatusRows(body)
	if err != nil {
		fmt.Fprintln(stderr, "yolo mcp-auth: status answer unreadable; relaunch with a current yolo build")
		return 1
	}
	if server != "" {
		filtered := []map[string]any{}
		for _, row := range rows {
			if row["name"] == server {
				filtered = append(filtered, row)
			}
		}
		if len(filtered) == 0 {
			diagnosis := mcpsigninNotOnce(server)
			filtered = append(filtered, diagnosis)
		}
		rows = filtered
	}
	body["once"] = rows
	if jsonForm {
		encoder := json.NewEncoder(stdout)
		if encoder.Encode(body) != nil {
			return 1
		}
		return 0
	}
	for _, row := range rows {
		verdict, _ := row["diagnosis"].(string)
		if verdict == "" {
			verdict, _ = row["state"].(string)
		}
		fmt.Fprintf(stdout, "%s: %s\n", row["name"], verdict)
		if step, _ := row["next_step"].(string); step != "" {
			fmt.Fprintln(stdout, step)
		}
		encoded, _ := json.Marshal(row)
		fmt.Fprintln(stdout, string(encoded))
	}
	return 0
}

func mcpStatusRows(body map[string]any) ([]map[string]any, error) {
	if body["schema"] != "yolo.mcp-auth.status/1" {
		return nil, errors.New("unknown schema")
	}
	raw, err := json.Marshal(body["once"])
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	err = json.Unmarshal(raw, &rows)
	return rows, err
}

func mcpsigninNotOnce(server string) map[string]any {
	return map[string]any{"name": server, "diagnosis": "not one-sign-in here", "next_step": server + ": make it one in your own config: yolo config set mcp_servers." + server + ".sign_in once --user, then relaunch"}
}
