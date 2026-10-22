package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/redteamstore"
)

// mcpAsk posts one JSON-RPC message to the red team's server and returns
// the parsed reply. The store is the test's own, empty before each test.
func mcpAsk(t *testing.T, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", RedteamMCPPath, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	New().Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var reply map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply %q isn't JSON: %v", rec.Body.String(), err)
	}
	return reply
}

// withRedteamStore points the server at a store of the test's own.
func withRedteamStore(t *testing.T) *redteamstore.DB {
	t.Helper()
	db, err := redteamstore.Open(filepath.Join(t.TempDir(), "redteam.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rdMu.Lock()
	before := rdDB
	rdDB = db
	rdMu.Unlock()
	t.Cleanup(func() {
		rdMu.Lock()
		rdDB = before
		rdMu.Unlock()
	})
	return db
}

// The server lists every tool the skills call, each with a schema an MCP
// client takes.
func TestRedteamMCPListsTheSkillsTools(t *testing.T) {
	fresh(t)
	withRedteamStore(t)
	reply := mcpAsk(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, _ := reply["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("no tool listed: %v", reply)
	}
	have := map[string]bool{}
	for _, e := range tools {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		have[name] = true
		schema, _ := json.Marshal(m["inputSchema"])
		if !json.Valid(schema) || !strings.Contains(string(schema), "\"type\":\"object\"") {
			t.Fatalf("%s has no object schema: %s", name, schema)
		}
	}
	for _, want := range []string{
		"redteam_engagement_begin", "redteam_asset_add", "redteam_asset_test", "redteam_asset_query",
		"redteam_asset_link", "redteam_asset_upsert", "redteam_vuln_add", "redteam_vuln_update",
		"redteam_credential_add", "redteam_credential_list", "redteam_access_add", "redteam_score_hit",
		"redteam_tunnel_add", "redteam_tunnel_update", "redteam_webshell_add", "redteam_session_check",
		"redteam_chain_add", "redteam_http_evidence_add", "redteam_attack_file_add", "redteam_poc_add",
		"redteam_poc_search", "redteam_poc_get", "redteam_web_list", "redteam_report",
	} {
		if !have[want] {
			t.Errorf("%s isn't listed", want)
		}
	}
}

// A call writes the store, and the next call reads what it wrote: the
// point of the server (an agent's finding outlives the turn).
func TestRedteamMCPToolCallWritesAndReads(t *testing.T) {
	fresh(t)
	db := withRedteamStore(t)
	begin := mcpAsk(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"redteam_engagement_begin","arguments":{"target":"单位X","scope":"lab"}}}`)
	content := toolText(t, begin)
	if !strings.Contains(content, "单位X") {
		t.Fatalf("the engagement answer doesn't name the target: %s", content)
	}
	add := mcpAsk(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"redteam_asset_add","arguments":{"target":"203.0.113.20:22","service":"ssh","provenance":"active"}}}`)
	if !strings.Contains(toolText(t, add), "203.0.113.20") {
		t.Fatalf("the asset answer: %s", toolText(t, add))
	}
	list, err := db.Assets("")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range list {
		if a.IP == "203.0.113.20" && a.Port == 22 && a.Service == "ssh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the call didn't reach the store: %+v", list)
	}
}

// A tool that fails answers isError with what went wrong, not a broken
// JSON-RPC reply: an agent must be able to read the reason.
func TestRedteamMCPToolErrorIsInTheResult(t *testing.T) {
	fresh(t)
	withRedteamStore(t)
	reply := mcpAsk(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"redteam_asset_add","arguments":{}}}`)
	result, _ := reply["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("a call without a target wasn't an error: %v", reply)
	}
	if !strings.Contains(toolText(t, reply), "target") {
		t.Fatalf("the error doesn't say what was missing: %s", toolText(t, reply))
	}
}

// A name no tool has is the JSON-RPC error for a bad parameter, as the
// other servers answer it.
func TestRedteamMCPUnknownTool(t *testing.T) {
	fresh(t)
	withRedteamStore(t)
	reply := mcpAsk(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"redteam_erase_everything","arguments":{}}}`)
	e, _ := reply["error"].(map[string]any)
	if e == nil {
		t.Fatalf("an unknown tool was answered as a result: %v", reply)
	}
	if code, _ := e["code"].(float64); code != -32602 {
		t.Fatalf("code %v, want -32602", e["code"])
	}
}

// The page a browser would make of it is refused: the server answers
// agents, not web pages.
func TestRedteamMCPRefusesAPage(t *testing.T) {
	fresh(t)
	withRedteamStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", RedteamMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Origin", "https://evil.example")
	New().Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("%d for a page's request, want 403", rec.Code)
	}
}

// toolText is the text of a tool call's answer.
func toolText(t *testing.T, reply map[string]any) string {
	t.Helper()
	result, _ := reply["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content: %v", reply)
	}
	m, _ := content[0].(map[string]any)
	s, _ := m["text"].(string)
	return s
}
