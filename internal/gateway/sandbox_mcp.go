package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/sandbox"
)

// The sandbox as an MCP server of magpie's own: Streamable HTTP at
// SandboxMCPPath, served and guarded as the red team's store server is
// (serveMCP), each call held to the calling gateway key's limit
// (reserveFor).
//
// It lists nothing while the plan is off: an agent that has the box off is
// not offered a tool that answers "the box is off". A command goes to the
// broker beside magpie (internal/sandbox), never to a container engine
// magpie itself holds.
//
//	sandbox_exec    run a command in a box
//	sandbox_boxes   what is running, and the ceiling
//	sandbox_reset   take every box away (the page's Restore)

// SandboxMCPPath is where the sandbox MCP server is served.
const SandboxMCPPath = "/mcp/magpie/sandbox"

// sandboxMCPBody is the longest body the server takes: a command line, and
// room for an image's name.
const sandboxMCPBody = 1 << 20

func (s *Server) sandboxMCP(w http.ResponseWriter, r *http.Request) {
	serveMCP(w, r, "magpie's sandbox", sandboxMCPBody, s.sandboxMCPCall)
}

type sandboxTool struct {
	Name, Title, Desc, Schema string
	Run                       func(ctx context.Context, args json.RawMessage) (string, error)
}

func sandboxTools() []sandboxTool {
	return []sandboxTool{
		{
			Name:  "sandbox_exec",
			Title: "在沙箱里执行命令",
			Desc:  "Run a shell command in a disposable container: no network unless the plan allows it, no host files mounted, capabilities dropped, memory and CPU bounded. Use it for untrusted code, binaries being reversed, and exploits being built. Answers the exit code, stdout and stderr; a non-zero exit is the command's answer, not an error.",
			Schema: `{"type":"object","properties":{
				"command":{"type":"string","description":"the shell command line to run in the box"},
				"image":{"type":"string","description":"a box from another image than the plan's (optional)"}
			},"required":["command"]}`,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Command string `json:"command"`
					Image   string `json:"image"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				p, err := sandbox.Load()
				if err != nil {
					return "", err
				}
				res, err := sandbox.NewClient().Run(ctx, p, sandbox.Exec{Command: in.Command, Image: in.Image})
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "exit %d\n", res.Exit)
				if s := strings.TrimRight(res.Stdout, "\n"); s != "" {
					b.WriteString("--- stdout ---\n")
					b.WriteString(s)
					b.WriteString("\n")
				}
				if s := strings.TrimRight(res.Stderr, "\n"); s != "" {
					b.WriteString("--- stderr ---\n")
					b.WriteString(s)
					b.WriteString("\n")
				}
				if res.Truncated {
					b.WriteString("[output cut at the broker's cap]\n")
				}
				return b.String(), nil
			},
		},
		{
			Name:   "sandbox_boxes",
			Title:  "沙箱状态",
			Desc:   "The sandbox plan, how many boxes run now, and the ceiling.",
			Schema: `{"type":"object","properties":{}}`,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				p, err := sandbox.Load()
				if err != nil {
					return "", err
				}
				running, max, err := sandbox.BrokerState(ctx, p)
				out := map[string]any{"plan": p, "running": running, "max": max}
				if err != nil {
					out["broker"] = err.Error()
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			},
		},
		{
			Name:   "sandbox_reset",
			Title:  "还原沙箱",
			Desc:   "Take every sandbox box away, including one a command still holds: the next call starts from the image again.",
			Schema: `{"type":"object","properties":{}}`,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				p, err := sandbox.Load()
				if err != nil {
					return "", err
				}
				n, err := sandbox.BrokerReset(ctx, p)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("removed %d boxes\n", n), nil
			},
		},
	}
}

func (s *Server) sandboxMCPCall(r *http.Request, m mcpMessage) (any, map[string]any) {
	plan, planErr := sandbox.Load()
	on := planErr == nil && plan.On
	switch m.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpVersion(m),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "magpie-sandbox", "title": "Magpie Sandbox", "version": Version},
			"instructions":    sandboxInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		if !on {
			return map[string]any{"tools": []any{}}, nil
		}
		list := make([]any, 0, len(sandboxTools()))
		for _, t := range sandboxTools() {
			list = append(list, map[string]any{
				"name":        t.Name,
				"title":       t.Title,
				"description": t.Desc,
				"inputSchema": json.RawMessage(t.Schema),
			})
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
		if !on {
			return toolResult(sandbox.ErrOff.Error(), true), nil
		}
		var tool *sandboxTool
		for i := range sandboxTools() {
			if t := sandboxTools()[i]; t.Name == p.Name {
				tool = &t
				break
			}
		}
		if tool == nil {
			return nil, map[string]any{"code": -32602, "message": "unknown tool " + p.Name + ": no sandbox tool has that name"}
		}
		release, err := reserveFor(access.Caller(r.Context()), int64(len(p.Arguments)), "", limitClock())
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		defer release()
		said, err := tool.Run(r.Context(), p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(said, false), nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + m.Method}
}

// sandboxInstructions is what an agent is told when it mounts the server.
const sandboxInstructions = `A box of its own for untrusted work: the command runs in a fresh container, not on this computer.
- Reach for sandbox_exec when a binary, a script or an exploit is not known good. The box's root is read-only, /work is tmpfs, capabilities are dropped, and it has no network unless the plan says so.
- The exit code is the command's. A tool that fails to start answers with the broker's error instead — then magpie's box is not running, and nothing was run here.
- sandbox_reset takes every box away; use it when an experiment should start from the image again.
- Nothing of magpie's is in the box: no /config, no keys, no docker socket. Copy in what the command needs as part of the command.`
