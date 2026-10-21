# Agent presets, fingerprints and proxy pools

Three things magpie's gateway gained after 0.1.1295, all of them one idea:
a request's path through the gateway is shaped by what the user says about
it, per agent (presets), per provider (fingerprint) and per exit (proxy
pool). None of them changes where a request is routed or which model it
asks; they change what goes out and through what.

## Agent presets (`internal/presets`, `plugins/presets`)

An agent preset is a named list of patches applied to the bodies an agent
sends, absorbed from DeepSeek Harness's agent presets (`agent.cordis.yml`,
`cordis.patch.yml`): dsh composes a session's prompt, context files and
tools out of plugin rows; magpie cannot compose an agent's process, so it
edits the request the agent sends, in the agent's own API.

| Part | What it is | Source |
| --- | --- | --- |
| Preset | `{id, name, sub, about, version, builtin, order, patches, isolate, disabled}` | [`presets.go`](../../internal/presets/presets.go) |
| Patch | `{id, kind, action, match, value, disabled, why}` | same |
| Kinds | `prompt` (system prompt sections), `context` (instruction files), `tools` (tool definitions) | same |
| Actions | prompt: `append`, `prepend`, `set`, `drop`; context: `allow`, `deny`, `compact`; tools: `allow`, `deny`, `rename`, `compact` | same |
| Apply | `Apply(Preset, proto, body) ([]byte, Stats)`, per API: `chat`, `anthropic`, `responses`, `gemini` | [`apply.go`](../../internal/presets/apply.go) |
| Stats | per kind `{applied, total, skipped}`, all kinds' totals, bytes in and out, why a patch was skipped | same |
| Store | `presets.json` in magpie's config folder: `custom`, `builtin` (edits of shipped presets), `agents` (the preset an agent gets), `default` | [`store.go`](../../internal/presets/store.go) |
| Shipped | `standard`, `minimal`, `ptc`, `redteam`, `reverse` | [`builtins.go`](../../internal/presets/builtins.go) |
| Gateway | `plugins/presets` registers a `gwmw.Agent` that applies the preset for `Info.Agent` and keeps the last 50 runs for the window | [`plugins/presets`](../../plugins/presets/presets.go) |
| API | `GET /api/presets`, `POST /api/presets/{save,copy,reset,remove,bind,try}` | [`presets_api.go`](../../internal/gui/presets_api.go) |
| Page | Agent presets (`presets`), cards with counters, the patch editor, the agents each preset is given to, and Try | [`assets/plugins/presets`](../../internal/gui/assets/plugins/presets/) |

Rules that hold, and are tested:

- A preset leaves a body byte-identical when it has nothing to say, or when
  every patch is off: `Apply` returns the same slice.
- A patch never changes the model, the stream flag, tool *declarations'
  names* as the vendor sees them (a rename is the model's view; the reply's
  call is the agent's), or anything routing reads.
- A patch that can't land is counted as skipped with a line saying why —
  no section by that name, no instruction file matching, no tool matching,
  an API that keeps no system prompt, or the same match already patched.
- A built-in may be edited (kept in `builtin`), reset, and copied; it may
  not be removed. A user's preset may be removed, and a binding to it goes
  with it.

## Fingerprints (`internal/fingerprint`)

A provider's `fingerprint` in `providers.json` names the client magpie's
requests to it imitate: the headers, the headers to drop, and whether the
body is shaped to that client too.

```json
"fingerprint": {
  "profile": "claude-code",
  "body": false,
  "override": ["x-app"]
}
```

- Built-ins: `claude-code`, `codex-cli`, `opencode`, `cline`, `generic`.
- Applied in `forwardOnce` after the account's own signing, so a profile's
  header wins over magpie's default and over the agent's passed-on one.
- Never replaced: `Authorization`, `Content-Type`, `Content-Length`,
  `Host`, `Connection`, `Transfer-Encoding`, `X-Magpie-*`. A profile that
  names one is refused at save.
- A signed-in account's request (Claude Code's, Codex's) keeps its own
  client's headers unless the profile says `"lock": true`.
- `body: true` drops the fields a client's own traffic never carries
  (`stream_options`, `user`, `metadata.user_id`) and nothing else; a body
  without them is returned as it came.

## Proxy pools (`internal/proxypool`)

A provider's `proxyPool` is several exits for one vendor:

```json
"proxyPool": {
  "mode": "round-robin",
  "failThreshold": 2,
  "rest": "60s",
  "exits": [{"url": "http://10.0.0.1:8080", "label": "hk"}, {"url": "socks5://10.0.0.2:1080"}]
}
```

- Modes: `round-robin`, `random` (by weight), `least-latency`, `sticky`.
- An exit is taken out of the rotation after `failThreshold` failures in a
  row (a connection failure, or a 429/5xx), for `rest`; a 4xx that isn't a
  rate limit is the vendor's answer, not the exit's, and rests nothing.
- Everything resting still yields an exit: the one that comes back soonest.
- A caller is pinned to an exit while it answers, by account, key or
  conversation, so a vendor's session affinity survives rotation. A
  request may pin one exit with `X-Magpie-Proxy: <label or address>`.
- A pool with no exit, or disabled, leaves the provider's `Proxy` and the
  global one deciding, exactly as before.
- `GET /api/providers/{id}/pool` reads each exit's state, picks, failures
  and mean latency this process has seen; `POST …/pool/rest` rests one by
  hand. The health is this process's: a restart forgets it, so
  `providers.json` stays the user's alone.

## Absorbed from dsh-purge (WoYun520), 2026-10-21

The redteam preset's prompt is the commander model: the lead plans,
dispatches, verifies returns and reports, and never acts itself; a
pre-flight lists missing resources before work starts; at most three
executors run at once and the default is sequential through five phases;
every dispatch brief opens with `redteamRole: <code>` and is
self-contained; the report discipline is deltas only, on four triggers;
the red lines (credentials are not accounts, a proxy on your own VPS is
not a tunnel, findings must reproduce) ride in the brief.

Not absorbed, and why:

- **SQLite asset store, scoring board, sidebar console** — client-plane
  machinery. A gateway preset edits request bodies; it cannot host a
  database service or a UI panel.
- **anchor-gate (bootstrap output cap)** — needs a max_tokens clamp per
  preset. Our patch kinds (prompt/context/tools) cannot touch max_tokens
  yet; adding a fourth kind is future work if red-team traffic ever
  needs it.
- **subagent spawner with role-tagged briefs** — the client owns
  subagents. Our commander prompt tells the model to write
  `redteamRole:` briefs into Task dispatches when its agent supports
  them, which works for Claude Code's Task tool without gateway changes.

The 18 redteam operational skills (recon-pipeline, nuclei-scan,
gogo/fscan-intranet, suo5/frp tunnels, webshell-toolkit, …) are in the
library from WoYun520/dsh-purge, given to claude and omp.
