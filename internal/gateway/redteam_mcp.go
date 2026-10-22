package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/redteamstore"
)

// The red team's record (internal/redteamstore) as an MCP server of
// magpie's own: Streamable HTTP at RedteamMCPPath, served and guarded as
// the search MCP server is (serveMCP), each call held to the calling
// gateway key's limit (reserveFor).
//
// It is open while the gateway serves, unlike the plugin tools server: the
// store is magpie's own and needs no plugin to be on. An agent that mounts
// it writes the engagement down as it runs, and the next session reads it
// back instead of being told in the conversation.
//
// The calls carry the target's addresses, credentials and commands, never
// a vendor's request, so masking has nothing to do with them; the agent
// decides what of the store it sends to a model afterwards.

// RedteamMCPPath is where the red team's MCP server is served.
const RedteamMCPPath = "/mcp/magpie/redteam"

// redteamMCPBody is the longest body the server takes: a response's
// evidence may carry a page's text.
const redteamMCPBody = 8 << 20

// redteamStore is the store the gateway's server writes: a test's own in
// its place. It is opened at the first call, so a magpie that never runs
// an engagement makes no database.
var redteamStore = func() (*redteamstore.DB, error) {
	rdMu.Lock()
	defer rdMu.Unlock()
	if rdDB != nil {
		return rdDB, nil
	}
	db, err := redteamstore.Open("")
	if err != nil {
		return nil, err
	}
	rdDB = db
	return rdDB, nil
}

func (s *Server) redteamMCP(w http.ResponseWriter, r *http.Request) {
	serveMCP(w, r, "magpie's red team store", redteamMCPBody, s.redteamMCPCall)
}

func (s *Server) redteamMCPCall(r *http.Request, m mcpMessage) (any, map[string]any) {
	switch m.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpVersion(m),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "magpie-redteam", "title": "Magpie Red Team Store", "version": Version},
			"instructions":    redteamInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]any, 0, len(redteamstore.Tools()))
		for _, t := range redteamstore.Tools() {
			e := map[string]any{
				"name":        t.Name,
				"title":       t.Title,
				"description": t.Desc,
				"inputSchema": json.RawMessage(t.Schema),
			}
			if !t.Mutates {
				e["annotations"] = map[string]any{"readOnlyHint": true}
			}
			list = append(list, e)
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": "invalid params: " + err.Error()}
		}
		t, ok := redteamstore.ToolNamed(p.Name)
		if !ok {
			return nil, map[string]any{"code": -32602, "message": "unknown tool " + p.Name + ": no red team tool has that name"}
		}
		release, err := reserveFor(access.Caller(r.Context()), int64(len(p.Arguments)), "", limitClock())
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		defer release()
		said, err := redteamRun(r.Context(), t, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(said, false), nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + m.Method}
}

// redteamRun runs one tool with a store, opening the store if need be.
func redteamRun(ctx context.Context, t redteamstore.Tool, args json.RawMessage) (string, error) {
	db, err := redteamStore()
	if err != nil {
		return "", err
	}
	var a map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
	}
	if a == nil {
		a = map[string]any{}
	}
	return t.Run(db, a)
}

// redteamInstructions is what an agent is told when it mounts the server:
// the order of the engagement, in the tool calls that write it down.
const redteamInstructions = `The record of a red-team engagement, written down as it runs.
1. Call redteam_engagement_begin with the target before anything else; every other call names it (or is empty for the newest).
2. Write every asset you find with redteam_asset_add (passive) or redteam_asset_upsert (active), and every attempt with redteam_asset_test, so the next role doesn't repeat it.
3. A finding needs evidence: redteam_vuln_add with the output that proves it, and redteam_http_evidence_add with the request and response when it is HTTP.
4. Credentials, accesses, tunnels, webshells and sessions go in as they are gained: redteam_credential_add, redteam_access_add, redteam_tunnel_add, redteam_webshell_add, redteam_session_check.
5. Every step is redteam_chain_add with the command as it ran and a summary of what came back.
6. Claim points once per host with redteam_score_hit; the target carries its port.
7. Search redteam_poc_search before writing an exploit; keep what worked with redteam_poc_add.
8. redteam_report is the check: read it before saying a step is done, and answer from it, not from memory.
Nothing here is deleted: a rescan appends, and an amend keeps what wasn't sent.`

var (
	rdMu sync.Mutex
	rdDB *redteamstore.DB
)
