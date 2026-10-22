# Red team store

An engagement is written down as it runs, in one SQLite database, so that a
later turn, a later session or another agent reads what an earlier one did
instead of being told it in a conversation. It is what
[Agent presets](agent-presets.md)'s `redteam` preset calls "记录": the
commander dispatches roles, and checks their reports against this store
rather than against memory.

The skills under `deploy/magpie/config/redteam-skills/` were written
against DeepSeek Harness's client-plane asset store (`redteam_asset_add`,
`redteam_vuln_add`, `redteam_credential_add`, …). Those tools were never
magpie's; this package is them, in magpie, served to every agent over MCP.

## Responsibilities and sources of truth

| Part | Responsibility | Source |
| --- | --- | --- |
| Database | `redteam.db` in magpie's config folder, WAL, `busy_timeout(5000)`, one connection (SQLite takes one writer). Made by the first `redteam_engagement_begin`, never before: a magpie that runs no engagement makes no database. | `Path`, `Open`, `do` in [`store.go`](../../internal/redteamstore/store.go) |
| Schema | Thirteen tables, each a thing found or gained: `engagements`, `assets`, `asset_edges`, `vulns`, `credentials`, `services_entries` (accesses), `tunnels`, `webshells`, `sessions`, `pocs`, `scores`, `chains`, `evidence`, `attack_files`. `CREATE TABLE IF NOT EXISTS` throughout, so an older database gains a table rather than failing. | `schema`, `migrate` in [`store.go`](../../internal/redteamstore/store.go) |
| Engagement | `Begin(target, scope, notes)`; the same target named twice returns the same engagement, since a skill names the unit on every call. `Target(idOrTarget)` also takes the target's own name, and an empty one is the newest engagement. | [`assets.go`](../../internal/redteamstore/assets.go) |
| Assets | `AddAsset` adds or amends by `(engagement, ip, port, scope)`. An amend keeps every field the caller didn't send and **appends** `test`, `surface`, `tags` and `names`: a rescan adds to what is there. An IPv4 address also gets its `/24` as an asset of its own (`scope: "segment"`), linked `contains` — the skills' "按 /24 自动建 C 段". | `AddAsset`, `linkSegment`, `Assets`, `AssetQuery` |
| Findings | `AddVuln` by `(target, name)` or by id; `AddCredential` by id, and otherwise `(target, user, secret_value)` — a dump read twice is one row. | [`findings.go`](../../internal/redteamstore/findings.go) |
| What is gained | `AddEntry` (accesses), `AddTunnel` (by listen address, amended on re-registration), `AddWebshell` (by URL), `AddSession` (by name; the same name again is a re-check). | `findings.go` |
| Points | `Score(category, target, points)`: one row per `(engagement, category, target)` — a host taken twice is worth its points once, as the skills say. A repeat answers `Already: true` with the first row's id. | `Score`, `Scores` |
| Actions and proof | `AddChain` (the command verbatim and its output's summary), `AddEvidence` (request and response), `AddAttackFile`. | `findings.go` |
| Techniques | `AddPoc` by name — the knowledge base outlives the engagement it was learned in, so `SearchPoc` reads every engagement's. | `AddPoc`, `SearchPoc`, `PocByName` |
| Report | `Report` rolls the engagement up (counts, points, the /24s, the last ten actions) with the lists the commander checks. | `Report` |
| Tools | The 27 `redteam_*` tools the skills and the preset call: schemas and handlers. 23 are the names the skills already use; `redteam_engagement_begin`, `redteam_assets`, `redteam_engagements` and `redteam_report` are the parts the skills had no tool for. | [`tools.go`](../../internal/redteamstore/tools.go) |
| MCP server | Streamable HTTP at `/mcp/magpie/redteam`, served and guarded as the web search's (`serveMCP`: a page is refused, another machine needs a gateway key), each call held to the calling gateway key's limit. Always on while the gateway serves: it needs no plugin. | [`redteam_mcp.go`](../../internal/gateway/redteam_mcp.go) |
| Page API | `GET /api/redteam` and `GET /api/redteam/report?engagement=…`, read-only. No engagement ever begun is `{"present": false, "why": …}`, not an empty table. | [`redteam_api.go`](../../internal/gui/redteam_api.go) |
| Library | The server is written into the agents that run an engagement (`magpie library mcp add magpie-redteam http://127.0.0.1:3425/mcp/magpie/redteam agents=claude,omp`), so Claude Code and omp see `mcp__magpie-redteam__redteam_*`. | `internal/library` |

## Rules that hold

- **Nothing is deleted.** A record is amended or appended to. No tool removes
  a row: a report that drops a finding would let the engagement say a thing
  never happened.
- **A failed read is not an empty store.** `Open` on a file that isn't a
  database fails, with the schema step named; the page says `present: false`
  only when no database was ever made. A report never shows "nothing found"
  because the file broke (code standards: a failed read means unknown).
- **An amend keeps what wasn't sent.** `updates` writes only the fields with
  a value, so `redteam_vuln_update` with a status can't blank the evidence.
- **A secret is kept as given.** A masked password is useless to the agent
  that reuses it; `redteam_credential_list` masks by default and
  `with_secrets: true` shows it for the one call that needs it.
- **The side of the boundary is part of an asset's identity.** `10.0.0.5:80`
  found from the internet and reached from inside are two rows; a `/24` is
  neither and takes `scope: "segment"`.

## Verification

```sh
go test ./internal/redteamstore/                      # 12 tests, no network
go test ./internal/gateway/ -run RedteamMCP
go test -tags nogui ./internal/gui/ -run Redteam
```

The store's tests cover: an unreadable file is an error; a reopened store
reads what the first wrote; `Begin` is idempotent by target; an asset is
amended rather than replaced; a `/24` is made once and linked twice; a
domain gets no segment; the two sides stay apart; a score counts once; a
credential and a vuln keep what an amend doesn't send; the lists read back;
a POC is found from another engagement; the report's counts.

## What was run for 426c0aea

```text
$ go test ./internal/redteamstore/
ok  	github.com/yetone/magpie/internal/redteamstore	0.616s   (12 tests)

$ CGO_ENABLED=0 go test ./internal/gateway/ -run RedteamMCP
ok  	github.com/yetone/magpie/internal/gateway	0.233s   (5 tests)

$ CGO_ENABLED=0 go test -tags nogui ./internal/gui/ -run Redteam
ok  	github.com/yetone/magpie/internal/gui	0.070s
```

Live, in the running container (`magpie:chenycl-redteam`), with a gateway
key, over `POST /mcp/magpie/redteam`:

```text
tools/list             → 27 tools
engagement_begin       → eng-1792655101019-0001
asset_add 203.0.113.77:443 → the host, and 203.0.113.0/24 linked "contains"
score_hit server-host  → 20 points; the same call again answers already
asset_test twice       → test is "smoke: 回写追加\n第二次尝试 → 401": appended
report                 → assets 2, internet 1, scores 20, scoreRows 1
GET /api/redteam       → present: true, the engagement
GET /api/redteam/report→ present: true, the same counts
```

The smoke engagement was dropped and `redteam.db` removed afterwards, so
the deployment starts with no engagement (the page says so, rather than
showing an empty table).

Not run: the whole suite, and a full five-phase engagement against a real
authorized target.
