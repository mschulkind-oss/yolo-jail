package packload

// mcpcompose_test.go pins the `mcp` kind's composition into mcp_servers
// (docs/design/mcp-presets-removal.md OQ-MP3, OQ-MP4): pack entries first, joined to the notch's
// home, the user's table merged over them per field, a user null removing one and staying as a
// removal, a name two packs ship held by the later one and reported, and the claim
// `yolo pack footprint` shows.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func mcpPack(t *testing.T, pack, server, command string, args ...string) *Pack {
	t.Helper()
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + a + `"`
	}
	return &Pack{Name: pack, Decl: declFrom(t, `{"contributes":[{"kind":"mcp","name":"`+server+
		`","bin":"`+server+`-bin","config":{"command":"`+command+`","args":[`+strings.Join(quoted, ",")+
		`],"env":{"PACK_VAR":"p","SHARED":"pack"}}}]}`)}
}

func userTable(t *testing.T, src string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("%s is not an object", src)
	}
	return m
}

func entryOf(t *testing.T, table *jsonx.OrderedMap, name string) *jsonx.OrderedMap {
	t.Helper()
	v, ok := table.Get(name)
	if !ok {
		t.Fatalf("the composed table has no %q: %s", name, dump(t, table))
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// A pack's entry lands under its name with every `~/` word joined to the home the caller renders
// for, and a word that only contains a tilde elsewhere is left as written.
func TestAPackMCPEntryIsJoinedToTheNotchsHome(t *testing.T) {
	p := mcpPack(t, "browserpack", "browser", "~/bin/server", "~/.local/share/x/wrapper", "--opt=~/not-a-path")
	for _, home := range []string{"/home/agent", "/Users/yolo-sandbox", "/home/you/"} {
		got := ComposeMCPServers(nil, []*Pack{p}, home)
		e := entryOf(t, got, "browser")
		root := strings.TrimSuffix(home, "/")
		if cmd, _ := e.Get("command"); cmd != root+"/bin/server" {
			t.Errorf("home %s: command = %v", home, cmd)
		}
		args, _ := e.Get("args")
		if list, _ := args.([]any); len(list) != 2 || list[0] != root+"/.local/share/x/wrapper" ||
			list[1] != "--opt=~/not-a-path" {
			t.Errorf("home %s: args = %v", home, args)
		}
	}
}

func TestComposeMCPServersDoesNotCarryHeadersAcrossDestinations(t *testing.T) {
	pack := &Pack{Name: "remote", Decl: declFrom(t, `{"contributes":[{"kind":"mcp","name":"linear","config":{"url":"https://old.example/mcp","headers":{"Authorization":"Bearer old","X-Pack":"secret"}}}]}`)}
	for _, tc := range []struct {
		name, override, want string
	}{
		{"host changed", `{"url":"https://new.example/mcp"}`, `{"url":"https://new.example/mcp"}`},
		{"scheme changed", `{"url":"http://old.example/mcp"}`, `{"url":"http://old.example/mcp"}`},
		{"port changed", `{"url":"https://old.example:9443/mcp"}`, `{"url":"https://old.example:9443/mcp"}`},
		{"path changed", `{"url":"https://old.example/other"}`, `{"url":"https://old.example/other"}`},
		{"explicit destination headers", `{"url":"https://new.example/mcp","headers":{"authorization":"Bearer new"}}`, `{"authorization":"Bearer new"}`},
		{"same destination preserves headers", `{"url":"https://old.example/mcp"}`, `{"Authorization":"Bearer old","X-Pack":"secret"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ComposeMCPServers(userTable(t, `{"linear":`+tc.override+`}`), []*Pack{pack}, "/h")
			entry := entryOf(t, got, "linear")
			headers, present := entry.Get("headers")
			if strings.Contains(tc.want, `"url"`) {
				if present {
					t.Fatalf("destination change retained inherited headers: %s", dump(t, entry))
				}
			} else {
				want, err := jsonx.Decode([]byte(tc.want))
				if err != nil {
					t.Fatal(err)
				}
				if !present || !reflect.DeepEqual(headers, want) {
					t.Fatalf("headers = %s, want %s", dump(t, headers), tc.want)
				}
			}
		})
	}
}

func TestComposeMCPServersBindsHeadersToEffectiveTransport(t *testing.T) {
	pack := &Pack{Name: "remote", Decl: declFrom(t, `{"contributes":[{"kind":"mcp","name":"linear","config":{"url":"https://mcp.example/mcp","headers":{"Authorization":"Bearer inherited","X-Pack":"pack"}}}]}`)}
	for _, tc := range []struct {
		name, override, wantAuthorization string
		wantHeader                        bool
	}{
		{name: "transport-only change clears inherited headers", override: `{"transport":"sse"}`},
		{name: "transport change keeps explicit replacement headers", override: `{"transport":"sse","headers":{"Authorization":"Bearer replacement"}}`, wantAuthorization: "Bearer replacement", wantHeader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ComposeMCPServers(userTable(t, `{"linear":`+tc.override+`}`), []*Pack{pack}, "/h")
			entry := entryOf(t, got, "linear")
			if transport, _ := entry.Get("transport"); transport != "sse" {
				t.Fatalf("effective transport = %v, want sse", transport)
			}
			headers, present := entry.Get("headers")
			if present != tc.wantHeader {
				t.Fatalf("headers = %s, present=%v, want present=%v", dump(t, headers), present, tc.wantHeader)
			}
			if tc.wantHeader {
				auth, ok := headers.(*jsonx.OrderedMap)
				if !ok {
					t.Fatalf("headers are not an object: %s", dump(t, headers))
				}
				if value, _ := auth.Get("Authorization"); value != tc.wantAuthorization {
					t.Fatalf("Authorization = %v, want %q", value, tc.wantAuthorization)
				}
			}
		})
	}

	server := &Pack{Name: "sse", Decl: declFrom(t, `{"contributes":[{"kind":"mcp","name":"linear","config":{"url":"https://mcp.example/mcp","transport":"sse","headers":{"Authorization":"Bearer inherited"}}}]}`)}
	got := ComposeMCPServers(userTable(t, `{"linear":{"url":"https://mcp.example/mcp"}}`), []*Pack{server}, "/h")
	entry := entryOf(t, got, "linear")
	if transport, _ := entry.Get("transport"); transport != "sse" {
		t.Fatalf("repeated same URL without transport lost inherited SSE transport: %v", transport)
	}
	if headers, present := entry.Get("headers"); !present || headers == nil {
		t.Fatalf("repeated same URL without transport lost inherited SSE headers: %s", dump(t, entry))
	}
}

func TestComposeMCPServersSupportsSafeStdioRemoteOverrides(t *testing.T) {
	remote := &Pack{Name: "remote", Decl: declFrom(t, `{"contributes":[{"kind":"mcp","name":"entry","config":{"url":"https://remote.example/mcp","headers":{"Authorization":"Bearer old"}}}]}`)}
	toStdio := ComposeMCPServers(userTable(t, `{"entry":{"url":null,"command":"node","args":["server.js"]}}`), []*Pack{remote}, "/h")
	stdio := entryOf(t, toStdio, "entry")
	if _, ok := stdio.Get("url"); ok {
		t.Fatalf("remote-to-stdio retained URL: %s", dump(t, stdio))
	}
	if _, ok := stdio.Get("headers"); ok {
		t.Fatalf("remote-to-stdio retained headers: %s", dump(t, stdio))
	}
	local := mcpPack(t, "local", "entry", "node", "server.js")
	toRemote := ComposeMCPServers(userTable(t, `{"entry":{"url":"https://remote.example/mcp"}}`), []*Pack{local}, "/h")
	remoteEntry := entryOf(t, toRemote, "entry")
	if _, ok := remoteEntry.Get("command"); ok {
		t.Fatalf("stdio-to-remote retained command: %s", dump(t, remoteEntry))
	}
	if _, ok := remoteEntry.Get("args"); ok {
		t.Fatalf("stdio-to-remote retained args: %s", dump(t, remoteEntry))
	}
}

// The user's table is the override layer: an entry of the same name merges over the pack's per
// field (env per variable), null removes the pack's and stays in the table as a removal, an entry
// no pack ships is copied, and nothing the caller handed in is written through.
func TestTheUserMCPTableComposesOverThePacks(t *testing.T) {
	packs := []*Pack{mcpPack(t, "a", "browser", "/bin/sh", "~/w"), mcpPack(t, "b", "gone", "/bin/sh", "~/g")}
	user := userTable(t, `{"browser":{"command":"/bin/sh","args":["~/mine"],"env":{"SHARED":"user","MINE":"1"}},
		"gone":null, "own":{"command":"npx","args":["-y","own-mcp"]}}`)
	before := dump(t, user)
	got := ComposeMCPServers(user, packs, "/home/agent")

	b := entryOf(t, got, "browser")
	if args, _ := b.Get("args"); dump(t, args) != `["~/mine"]` {
		t.Errorf("your args replace the pack's whole (the user layer is not joined): %s", dump(t, args))
	}
	env, _ := b.Get("env")
	if s := dump(t, env); !strings.Contains(s, `"PACK_VAR": "p"`) || !strings.Contains(s, `"SHARED": "user"`) ||
		!strings.Contains(s, `"MINE": "1"`) {
		t.Errorf("env merges per variable, yours winning: %s", s)
	}
	if v, ok := got.Get("gone"); !ok || v != nil {
		t.Errorf("a user null must remove the pack's entry and stay a removal (the jail's presets "+
			"lose that name too): %v, %v", v, ok)
	}
	if own := entryOf(t, got, "own"); own == nil {
		t.Error("an entry no pack ships was dropped")
	}
	if dump(t, user) != before {
		t.Errorf("composing wrote through the caller's table:\nbefore %s\nafter  %s", before, dump(t, user))
	}
	// And the composed value is not the caller's either.
	o := entryOf(t, got, "own")
	o.Set("command", "changed")
	if dump(t, user) != before {
		t.Error("an edit of the composed table reached the caller's")
	}
}

// Two packs shipping one server name: the composer holds the later declaration (NC-D59), and the
// name is reported as a collision both by the message validation refuses with and by the
// footprint's generic exclusive loop.
func TestAnMCPNameTwoPacksShipIsHeldByTheLaterAndReported(t *testing.T) {
	first := mcpPack(t, "first", "browser", "/first")
	later := mcpPack(t, "later", "browser", "/later")
	got := ComposeMCPServers(nil, []*Pack{first, later}, "/h")
	if cmd, _ := entryOf(t, got, "browser").Get("command"); cmd != "/later" {
		t.Errorf("command = %v, want the later pack's", cmd)
	}
	msgs := MCPNameCollisions([]*Pack{first, later})
	if len(msgs) != 1 || !strings.Contains(msgs[0], `MCP server "browser" is shipped by packs first and later`) ||
		!strings.Contains(msgs[0], "Remove one of those packs from `packs`") {
		t.Errorf("collision messages = %v", msgs)
	}
	// The steps are the verbs, and the file is the user config that exists, never a hard-coded
	// legacy config.jsonc.
	for _, want := range []string{"`yolo pack remove <pack>`", "`yolo config set mcp_servers.browser.<field> <value> --user`"} {
		if len(msgs) == 1 && !strings.Contains(msgs[0], want) {
			t.Errorf("collision message lacks %q: %s", want, msgs[0])
		}
	}
	if MCPNameCollisions([]*Pack{first, mcpPack(t, "other", "other", "/o")}) != nil {
		t.Error("two different names reported as a collision")
	}
	found := false
	for _, c := range Collisions([]*Pack{first, later}) {
		if c.Kind == packdecl.KindMCP && c.Target == "browser" {
			found = true
		}
	}
	if !found {
		t.Errorf("packload.Collisions does not report the server name: %+v", Collisions([]*Pack{first, later}))
	}
}

// The footprint names the claim, with the command the agent's client starts and the program.
func TestTheMCPClaimNamesTheCommand(t *testing.T) {
	fp := FootprintOf(mcpPack(t, "browserpack", "browser", "/bin/sh", "~/w"))
	for _, c := range fp.Claims {
		if c.Kind == packdecl.KindMCP {
			if c.Target != "browser" || c.ReviewWorthy || c.Detail != "runs /bin/sh ~/w (program browser-bin)" {
				t.Errorf("mcp claim = %+v", c)
			}
			return
		}
	}
	t.Errorf("no mcp claim in %+v", fp.Claims)
}

func TestComposeMCPServersLowersOffToRemoval(t *testing.T) {
	packs := []*Pack{mcpPack(t, "linear", "linear", "npx")}
	got := ComposeMCPServers(userTable(t, `{"linear":"off"}`), packs, "/home/agent")
	value, present := got.Get("linear")
	if !present || value != nil {
		t.Fatalf("off removal = %v, present=%v; want a nil tombstone", value, present)
	}
}

// MCPServerSources names the pack each composed server starts from, says when yours merges over
// it, and leaves out one your null or "off" removes.
func TestMCPServerSourcesNamesThePack(t *testing.T) {
	packs := []*Pack{
		mcpPack(t, "a", "browser", "/x"), mcpPack(t, "b", "other", "/y"),
		mcpPack(t, "c", "gone-null", "/z"), mcpPack(t, "d", "gone-off", "/w"),
	}
	got := MCPServerSources(userTable(t, `{"other":{"command":"/mine"},"gone-null":null,"gone-off":"off"}`), packs)
	want := []string{"browser (pack a)", "other (pack b, your mcp_servers merged over it)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("MCPServerSources = %q, want %q", got, want)
	}
}
