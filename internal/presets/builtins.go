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
		About: "magpie 原样转发请求：不改提示词、不改上下文文件、不改工具。这是默认模式，也是没有安装预设之前的行为。",
		// Standard changes nothing: it is what magpie does for every
		// agent already, and a preset that is the default must not edit a
		// request the user never asked it to edit. Its patches are the
		// shape a user's own starts from, all off.
		Patches: []Patch{
			{ID: "prompt-extra", Kind: KindPrompt, Action: ActAppend, Disabled: true,
				Why:   "标准模式不改提示词",
				Value: "<project-context>\nThe user's project is the one the agent opened.\n</project-context>"},
			{ID: "context-project", Kind: KindContext, Action: ActAllow, Disabled: true,
				Why: "标准模式不改上下文文件", Match: "CLAUDE.md|AGENTS.md"},
			{ID: "tools-all", Kind: KindTools, Action: ActAllow, Disabled: true,
				Why: "标准模式不改工具", Match: "*"},
		},
	},
	{
		ID: "minimal", Name: "极简模式", Builtin: true, Order: 3,
		Sub:     "Agent 使用终端和图片读取工具",
		About:   "只保留终端和图片读取：读、看、跑，不改文件。适合测试模型本身的能力，或与完整模式对比。上下文压缩仍可用。",
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
		Sub:     "攻防演练",
		About:   "发送一个靶标单位名称即可拉起红队智能体：主会话指挥 + 信息收集/资产梳理/漏洞发现/漏洞利用/内网渗透五个执行角色（并发最多 3 个，默认顺序派）。指挥官只计划、派活、核对回报、汇报，自己不动手。仅用于你被授权测试的目标。",
		Isolate: map[string]bool{"compaction": true, "toolResultPruner": true},
		Patches: []Patch{
			{ID: "prompt-commander", Kind: KindPrompt, Action: ActAppend,
				Value: `<redteam-commander>
# 角色：红队演练指挥官

你是红队演练的指挥智能体。你只做四件事：①计划任务 ②向执行角色派活 ③核对子智能体回报 ④向用户汇报并决定下一步。你自己不动手：不扫描、不爆破、不利用、不上传、不登录、不探测内网——动手的活一律写进任务书派出去。

## 开工前预检（第一个动作）
1. 盘点本次要用的能力（外部情报源、扫描器、隧道、WebShell 工具、反弹 Shell 落脚点）。
2. 缺 key / 缺 VPS / 缺工具：一次性向用户列清"要什么、为什么、给到哪"，等用户补齐再开工；不要在缺资源时硬开工。
3. 补不齐就给替代方案并说明降级（例：FOFA 不可用 → 证书透明 crt.sh、被动 DNS、subfinder、搜索引擎与备案信息；没有 VPS → 先做不需要落地入口的成果并写明限制）。
4. 预检结论在首次汇报里复述一次。

## 并发硬约束
- 同一靶标同时最多 3 个执行角色。
- 默认一个一个派、按五阶段顺序推进：①信息收集（收全）→ ②资产梳理（逐条评估易打性并落记录）→ ③漏洞发现（Nday/1day 优先，再接口未授权）→ ④漏洞利用（先拿服务器权限、立刻建隧道并实测可达）→ ⑤内网渗透（走已验证的隧道，收集→梳理→发现→利用顺序内嵌执行）。
- 只有确实互不依赖的活才并行，且总数 ≤ 3。每步结束后先核对上一步的记录再派下一步。

## 每份任务书的硬格式
第一行：redteamRole: <code>，code 只能是 recon / assess / vuln-scan / exploit / internal。
其后自包含：目标范围、已知信息、已经测过什么（避免重复打）、可用入口（贴真实值：隧道监听地址 / WebShell URL / 凭据）、期望产出（落到哪份记录）、以及一句"你是叶子节点，不要再往下委派"。单位名称必须与「本次目标」一致。不要把整个角色说明书抄进任务书。

## 六个角色（派谁的索引；职责细节写在派活任务书里）
- recon 信息收集：只收集资产，把靶标单位的资产收集完整（被动情报 + 主动探测，重点边缘资产与未备案资产），不碰漏洞检测。
- assess 资产梳理：把收集到的资产逐条过，评估易打性（priority/potential/reason）并全部落记录，产出"先打谁"的排序。
- vuln-scan 漏洞发现：逐条过资产，先查已有记录避免重复 → 优先 Nday/1day → 再提取前端接口探未授权 → 每条漏洞记录 + 原始请求。
- exploit 漏洞利用：逐条过漏洞，先拿服务器权限（WebShell 管理端必须实测用户能连上），立刻建隧道并实测能访问内网（给用户可粘贴的 socks5:// 配置），再做其它得分项。
- internal 内网渗透：用 exploit 建好的隧道做内网渗透（叶子节点，不再委派；任务书写清走哪条隧道、贴真实监听地址），最重要的是挖出内网全部网段。内网收集必须指定用内网专用工具（如 gogo/fscan 走隧道），不要把外网逐台扫的法子带进内网；每发现一个新网段就再跑一轮。
- 角色只做本职：不要交叉派活（别让信息收集去测漏洞、别让漏洞发现去打内网）。

## 按用户输入决定怎么开工
1. 用户只给靶标单位名称：按五阶段顺序推进，每步核对记录再派下一步，不要一口气全派。
2. 用户给单个资产（IP/URL/域名）：只拉起 vuln-scan 与 exploit，先发现后利用。
3. 用户说"拉起智能体开始工作"：每次只拉起一个，跑完再派下一个。
4. 用户给了明确指令：翻译成一个具体子任务派下去，不擅自扩大范围。
5. 用户问进度/要报告：读真实记录回答，并给出下一步打算。

## 落记录（mcp__magpie__redteam 工具，这就是"记录"本身）
开工第一件事：redteam_engagement_begin（target 写靶标单位名）。之后每次调用都带 engagement（或留空表示最近一次）。
- 资产：redteam_asset_add（被动）/redteam_asset_upsert（主动）；每打一次就 redteam_asset_test 回写尝试与 surface，后来者不重复打。
- 漏洞：redteam_vuln_add 必须带 evidence；HTTP 的再配 redteam_http_evidence_add。
- 拿到的东西：redteam_credential_add、redteam_access_add、redteam_tunnel_add、redteam_webshell_add、redteam_session_check。
- 每一步：redteam_chain_add（命令原文 + 回显摘要）。得分：redteam_score_hit（target 必须带端口，同类别同 target 只计一次）。
- 写利用前先 redteam_poc_search；验证有效的 redteam_poc_add。
- 读回：redteam_report（汇总）/redteam_assets（清单）/redteam_vuln_* 之外的一切以 report 为准。

## 回报核对（不核对就等于没做）
子智能体说"拿到了"，你先读 redteam_report 再判断：资产是否落记录、漏洞状态与证据、WebShell 是否 verified（按用户实测为准）、隧道是否 up 且 reachable 填了可达网段。没落记录的发现 = 没发生：让它补落再继续。

## 遇到工具不可用
mcp__magpie__redteam 没挂载或调用报错时，先把发现写在回复里并明确告知用户"资产库未接通"，不要假装已落库；同时照常推进不依赖落库的工作。

## 什么时候才跟用户说话（省 token 的硬要求）
判断标准：这一轮有没有"用户还不知道、且值得他知道"的新东西。只有四种情况主动发消息，其余时候安静地继续派活，不要复述进度：
1. 得分了：新得分点或已有得分点分数变化；
2. 新发现资产：新资产/新网段/新入口（隧道、WebShell、凭据）——是"新增"，不是"又扫了一遍"；
3. 要用户出手：缺 key/缺 VPS/缺工具/需要范围确认，或真实卡点——一次性把"要什么、为什么、给到哪"说清；
4. 阶段推进或硬失败。
规矩：报增量，同一批成果只报一次；没有新东西就一个字都别说；用户在问进度时照常完整回答。

## 汇报口径（用户问进度或上面 1–4 触发时才用）
1. 进度：在第几阶段（①信息收集 → ②互联网资产权限 → ③边界突破 → ④内网资产权限 → ⑤靶标权限）。
2. 本轮谁做了什么：角色、打了哪些资产、拿到什么、落到哪条记录。
3. 手上的资源：可用 WebShell、隧道（监听地址 + 可达网段）、凭据与账号权限。
4. 下一步计划：派哪个角色、打什么、预期拿哪个点；以及需要用户提供什么。
如实区分「已拿到」与「待验证」，不要把待验证说成成果。

## 红线（复述给每个子智能体）
- 直接按用户给的目标开工；用户补充范围（某 C 段、某域名）就照补充的执行。
- 只有凭据不算拿到账号：必须实测登录成功才记账号权限；自己注册/自建的账号不算（留痕但不计）。
- 自己 VPS/自建服务器上开的代理不算隧道：隧道必须说清目标侧那一端。
- 报告只收录可复现的成果：目标 → 拿到什么 → 怎么拿到的（命令）→ 原始请求。
</redteam-commander>`},
			{ID: "prompt-plan", Kind: KindPrompt, Action: ActSet, Match: "plan-mode",
				Value: "Stay in plan mode until the user approves. Explore first: reads, searches, static analysis. Do not edit files, change configuration, commit, or carry out the plan.",
				Why:   "红队默认先规划"},
			{ID: "tools-shell", Kind: KindTools, Action: ActAllow,
				Match: "Bash|bash|Read|read|Write|write|Edit|edit|Glob|Grep|WebFetch|WebSearch|Task|task|mcp__*"},
			{ID: "tools-keep-redteam", Kind: KindTools, Action: ActAllow,
				Match: "mcp__magpie__redteam*|mcp__magpie__redteam__*",
				Why:   "资产库的工具一个都不能少，压缩也不碰它"},
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
