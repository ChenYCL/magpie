package presets

// The presets magpie ships, after DeepSeek Harness's agent presets
// (apps/cli/config/agent-presets and packages/bundle/web-app/presets):
// a standard one with every tool, a minimal one that reads and runs
// commands but never edits, and two of ours, which dsh keeps as a user's
// own: a red-team one and a reverse-engineering one.
//
// A preset here edits the bodies agents send through the gateway, so the
// same preset applies to Claude Code, Codex, OpenCode and the rest: what
// differs between them is the API their bodies are in, not the intent.
// The tool names are the agents' own (Read, Edit, Bash for Claude Code;
// read, edit, bash for OpenCode), which is why a patch's match takes
// several names at once ("Read|ReadFile|read").

var builtins = []Preset{
	{
		ID: "standard", Name: "标准模式", Builtin: true, Order: 1,
		Sub:   "新任务默认",
		About: "处理代码、文件和资料的默认方式。Agent 按需使用检索、编辑等工具，能在一次会话里读、改、跑、验证。",
		Patches: []Patch{
			{ID: "prompt-runtime", Kind: KindPrompt, Action: ActAppend,
				Value: "<runtime-context>\nThe user's project is the one the agent opened. Prefer the tools you have over new machinery; read before writing; verify what you changed.\n</runtime-context>"},
			{ID: "context-project", Kind: KindContext, Action: ActAllow, Match: "CLAUDE.md|AGENTS.md|*.md"},
			{ID: "tools-all", Kind: KindTools, Action: ActAllow, Match: "*"},
		},
	},
	{
		ID: "minimal", Name: "极简模式", Builtin: true, Order: 3,
		Sub:   "Agent 使用终端和图片读取工具",
		About: "只保留终端和图片读取：读、看、跑，不改文件。适合测试模型本身的能力，或与完整模式对比。上下文压缩仍可用。",
		Isolate: map[string]bool{"compaction": true},
		Patches: []Patch{
			{ID: "prompt-minimal", Kind: KindPrompt, Action: ActSet, Match: "runtime-context",
				Value: "You are a helpful software engineer assistant. Use the shell to explore; do not edit files."},
			{ID: "context-off", Kind: KindContext, Action: ActDeny, Match: "*",
				Why: "极简模式不读项目说明文件"},
			{ID: "tools-allow", Kind: KindTools, Action: ActAllow, Match: "Bash|bash|Read|read|ReadImage|read_image|Glob|Grep"},
			{ID: "tools-compact", Kind: KindTools, Action: ActCompactTools, Match: "Bash|bash|Read|read"},
		},
	},
	{
		ID: "ptc", Name: "PTC 模式", Builtin: true, Order: 4,
		Sub:   "提示词即代码",
		About: "不给工具定义，让模型在正文里写出调用；适合验证模型自身的工具调用能力，代价是不能流式返回工具结果。",
		Patches: []Patch{
			{ID: "prompt-ptc", Kind: KindPrompt, Action: ActAppend,
				Value: "<tool-calling>\nYou have no tool definitions. When you need one, write the call in a fenced block labelled with the tool's name and a JSON body, then wait for the result.\n</tool-calling>"},
			{ID: "tools-none", Kind: KindTools, Action: ActDeny, Match: "*"},
		},
	},
	{
		ID: "redteam", Name: "红队模式", Builtin: true, Order: 5,
		Sub:   "攻防演练",
		About: "发送一个靶标单位名称即可拉起红队智能体：主会话加信息收集、资产梳理、漏洞发现、漏洞利用、内网渗透五个执行角色（并发最多 3 个）。仅用于你被授权测试的目标。",
		Isolate: map[string]bool{"compaction": true, "toolResultPruner": true},
		Patches: []Patch{
			{ID: "prompt-roles", Kind: KindPrompt, Action: ActAppend,
				Value: "<redteam-roles>\nYou run a red-team engagement for the target the user names, and only that target. Plan as a lead, then hand work to at most three roles at a time: recon, asset inventory, vulnerability discovery, exploitation, lateral movement. Record every finding with its evidence; never act on a host the user has not named.\n</redteam-roles>"},
			{ID: "prompt-plan", Kind: KindPrompt, Action: ActSet, Match: "plan-mode",
				Value: "Stay in plan mode until the user approves. Explore first: reads, searches, static analysis. Do not edit files, change configuration, commit, or carry out the plan.",
				Why: "红队默认先规划"},
			{ID: "tools-shell", Kind: KindTools, Action: ActAllow,
				Match: "Bash|bash|Read|read|Write|write|Edit|edit|Glob|Grep|WebFetch|WebSearch|Task|task|mcp__*"},
			{ID: "tools-compact-mcp", Kind: KindTools, Action: ActCompactTools, Match: "mcp__*",
				Why: "红队常用的 MCP 工具集很大，压缩 schema 以省上下文"},
		},
	},
	{
		ID: "reverse", Name: "逆向模式", Builtin: true, Order: 6,
		Sub:   "标准模式 + 逆向工具",
		About: "标准模式加上二进制与固件分析的工具集（紧凑 schema）。平时不用；目标是一份样本或固件时再切换。",
		Patches: []Patch{
			{ID: "prompt-reverse", Kind: KindPrompt, Action: ActAppend,
				Value: "<reverse-engineering>\nRead binaries as data first: identify the format, the architecture and the entry points before running anything. Never execute an untrusted sample on this machine; use the sandbox the tools give you.\n</reverse-engineering>"},
			{ID: "tools-reverse", Kind: KindTools, Action: ActAllow,
				Match: "Bash|bash|Read|read|Write|write|Edit|edit|Glob|Grep|Task|task|mcp__*"},
			{ID: "tools-compact", Kind: KindTools, Action: ActCompactTools, Match: "mcp__*"},
			{ID: "context-project", Kind: KindContext, Action: ActAllow, Match: "CLAUDE.md|AGENTS.md|*.md"},
		},
	},
}

// shippedOf is a built-in as it ships, nil for an id that isn't one.
func shippedOf(id string) *Preset {
	for i := range builtins {
		if builtins[i].ID == id {
			p := builtins[i]
			return &p
		}
	}
	return nil
}

// Builtins lists the presets magpie ships, cards' order.
func Builtins() []Preset {
	out := make([]Preset, 0, len(builtins))
	for _, p := range builtins {
		q := p.Clone()
		q.Builtin = true
		out = append(out, q)
	}
	return Sorted(out)
}
