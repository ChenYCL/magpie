package redteamstore

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Tool is one of the redteam_* tools an agent calls.
type Tool struct {
	Name    string
	Title   string
	Desc    string
	Schema  string // JSON Schema of the arguments
	Run     func(d *DB, args map[string]any) (string, error)
	Mutates bool // whether a call changes the store
}

// Tools are every tool the red team's skills name, in the order the MCP
// server lists them: the engagement first, then what is found, then what
// is gained, then what is learned.
func Tools() []Tool {
	return []Tool{
		{Name: "redteam_engagement_begin", Title: "开始一次演练",
			Desc: "Open the engagement for a target (the unit's name). Calling it again for the same target returns the same engagement. Call this before any other redteam tool.",
			Schema: obj(objProps{
				"target": {"type": "string", "description": "the unit, domain or system under test"},
				"scope":  {"type": "string", "description": "what the operator authorized, verbatim"},
				"notes":  {"type": "string", "description": "anything else the engagement needs"},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				eng, err := d.Begin(str(a, "target"), str(a, "scope"), str(a, "notes"))
				return jsonOf(eng), err
			}},

		{Name: "redteam_assets", Title: "资产清单",
			Desc: "List an engagement's assets (hosts and the /24 of each), newest state first.",
			Schema: obj(objProps{
				"engagement": {"type": "string", "description": "the engagement (or its target); empty is the newest"},
				"query":      {"type": "string", "description": "host, host:port, service or a word in the title; empty lists all"},
				"format":     {"type": "string", "enum": []string{"json", "table"}, "description": "json (default) or a compact table"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.AssetQuery(str(a, "engagement"), str(a, "query"))
				if err != nil {
					return "", err
				}
				if str(a, "format") == "table" {
					return assetTable(list), nil
				}
				return jsonOf(list), nil
			}},

		{Name: "redteam_asset_add", Title: "记录资产",
			Desc: "Record an asset, or amend the one at that host:port. What a rescan finds is appended to what is there (test, surface, tags, names), never replaces it. An IPv4 address also gets its /24 as an asset, linked by \"contains\".",
			Schema: obj(objProps{
				"engagement": {"type": "string", "description": "the engagement or its target"},
				"target":     {"type": "string", "description": "host, host:port or URL"},
				"names":      {"type": "string", "description": "domains that resolve here, comma separated"},
				"service":    {"type": "string", "description": "ssh | http | redis | …"},
				"title":      {"type": "string", "description": "the page or banner title"},
				"product":    {"type": "string", "description": "the server or product identified"},
				"provenance": {"type": "string", "enum": []string{"passive", "active"}, "description": "found without touching it, or by probing it"},
				"tool":       {"type": "string", "description": "the tool that found it"},
				"scope":      {"type": "string", "enum": []string{"internet", "internal"}, "description": "which side of the boundary (default internet)"},
				"tags":       {"type": "string", "description": "edge, test, dev, pre, segment…"},
				"priority":   {"type": "string", "description": "high | medium | low"},
				"potential":  {"type": "string", "description": "how easy to take"},
				"reason":     {"type": "string", "description": "why that priority"},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				as, err := d.AddAsset(str(a, "engagement"), AssetIn{
					Target: str(a, "target"), Names: str(a, "names"), Service: str(a, "service"),
					Title: str(a, "title"), Product: str(a, "product"), Provenance: str(a, "provenance"),
					Tool: str(a, "tool"), Scope: str(a, "scope"), Tags: str(a, "tags"),
					Priority: str(a, "priority"), Potential: str(a, "potential"), Reason: str(a, "reason"),
					Status: str(a, "status"),
				})
				return jsonOf(as), err
			}},

		{Name: "redteam_asset_test", Title: "回写测试结果",
			Desc: "Write back what was tried on an asset and what it turned up, so the next role doesn't repeat it. test and surface are appended, not replaced. status=abandoned with blocked=true after the target blocked the work more than three times.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string", "description": "host:port the attempt was against"},
				"status":     {"type": "string", "enum": []string{"new", "testing", "owned", "abandoned"}},
				"test":       {"type": "string", "description": "what was tried, one line, with its outcome"},
				"surface":    {"type": "string", "description": "what the attempt found to attack next (a login page, an API, a version)"},
				"blocked":    {"type": "boolean", "description": "the target blocked this address"},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				var blocked *bool
				if v, ok := a["blocked"].(bool); ok {
					blocked = &v
				}
				as, err := d.TestAsset(str(a, "engagement"), str(a, "target"), str(a, "status"),
					str(a, "test"), str(a, "surface"), blocked)
				return jsonOf(as), err
			}},

		{Name: "redteam_asset_query", Title: "查资产",
			Desc: "Find assets by host, host:port, service or a word in the title.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"query":      {"type": "string", "description": "host, host:port, service or title word"},
			}, "query"), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.AssetQuery(str(a, "engagement"), str(a, "query"))
				return jsonOf(list), err
			}},

		{Name: "redteam_asset_link", Title: "建立资产关系",
			Desc: "Record a relation between two assets: resolves (domain → IP), contains (/24 → IP), same-cert, redirects.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"from":       {"type": "string", "description": "host or host:port of the source"},
				"to":         {"type": "string", "description": "host or host:port of the target"},
				"rel":        {"type": "string", "description": "resolves | contains | same-cert | redirects | related"},
			}, "from", "to"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				if err := d.LinkAssets(str(a, "engagement"), str(a, "from"), str(a, "to"), str(a, "rel")); err != nil {
					return "", err
				}
				return `{"ok":true}`, nil
			}},

		{Name: "redteam_asset_upsert", Title: "记录主动探测结果",
			Desc: "The same as redteam_asset_add, for a finding that came from probing the host (provenance set to active unless given).",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string"},
				"names":      {"type": "string"},
				"service":    {"type": "string"},
				"title":      {"type": "string"},
				"product":    {"type": "string"},
				"tool":       {"type": "string"},
				"scope":      {"type": "string", "enum": []string{"internet", "internal"}},
				"tags":       {"type": "string"},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				prov := str(a, "provenance")
				if prov == "" {
					prov = "active"
				}
				as, err := d.AddAsset(str(a, "engagement"), AssetIn{
					Target: str(a, "target"), Names: str(a, "names"), Service: str(a, "service"),
					Title: str(a, "title"), Product: str(a, "product"), Provenance: prov,
					Tool: str(a, "tool"), Scope: str(a, "scope"), Tags: str(a, "tags"),
				})
				return jsonOf(as), err
			}},

		{Name: "redteam_web_list", Title: "列 Web 资产",
			Desc: "List the engagement's web assets (those with a service of http/https or a URL), which the fingerprinting step works through.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.AssetQuery(str(a, "engagement"), "")
				if err != nil {
					return "", err
				}
				var web []Asset
				for _, as := range list {
					s := strings.ToLower(as.Service + " " + as.URL + " " + as.Title)
					if as.URL != "" || strings.Contains(s, "http") || as.Port == 80 || as.Port == 443 ||
						as.Port == 8080 || as.Port == 8443 {
						web = append(web, as)
					}
				}
				return jsonOf(web), nil
			}},

		{Name: "redteam_vuln_add", Title: "记录漏洞",
			Desc: "Record a vulnerability, or amend one on that target by name. A finding needs evidence: the response or the command's output.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string", "description": "host:port it is on"},
				"name":       {"type": "string", "description": "the vulnerability, e.g. Redis 未授权访问"},
				"kind":       {"type": "string", "description": "n-day | 1-day | unauth | weak-cred | config"},
				"severity":   {"type": "string", "enum": []string{"critical", "high", "medium", "low", "info"}},
				"confidence": {"type": "string", "enum": []string{"confirmed", "likely", "suspected"}},
				"gained":     {"type": "string", "description": "what it got, e.g. 数据库权限/可写文件"},
				"evidence":   {"type": "string", "description": "the output that proves it"},
				"status":     {"type": "string", "enum": []string{"open", "exploited", "fixed", "false-positive"}},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				v, err := d.AddVuln(str(a, "engagement"), VulnIn{
					ID: str(a, "id"), Target: str(a, "target"), Name: str(a, "name"), Kind: str(a, "kind"),
					Severity: str(a, "severity"), Confidence: str(a, "confidence"), Gained: str(a, "gained"),
					Evidence: str(a, "evidence"), Status: str(a, "status"),
				})
				return jsonOf(v), err
			}},

		{Name: "redteam_vuln_update", Title: "更新漏洞",
			Desc: "Amend a vulnerability by its id: its status, what it gained, more evidence.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"id":         {"type": "string", "description": "the vulnerability's id"},
				"status":     {"type": "string", "enum": []string{"open", "exploited", "fixed", "false-positive"}},
				"gained":     {"type": "string"},
				"evidence":   {"type": "string"},
				"severity":   {"type": "string"},
			}, "id"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				v, err := d.AddVuln(str(a, "engagement"), VulnIn{
					ID: str(a, "id"), Status: str(a, "status"), Gained: str(a, "gained"),
					Evidence: str(a, "evidence"), Severity: str(a, "severity"),
				})
				return jsonOf(v), err
			}},

		{Name: "redteam_credential_add", Title: "记录凭据",
			Desc: "Keep a credential that works, in plaintext (an agent reuses it on the next host). The same secret on the same host is one row.",
			Schema: obj(objProps{
				"engagement":   {"type": "string"},
				"target":       {"type": "string", "description": "host:port it works on"},
				"user":         {"type": "string"},
				"secret_kind":  {"type": "string", "description": "password | hash | key | token | cookie"},
				"secret_value": {"type": "string", "description": "the password, hash or token itself"},
				"source":       {"type": "string", "description": "where it came from (hydra, a dump, config)"},
				"secret_ref":   {"type": "string", "description": "the runs/ file with the evidence"},
				"privilege":    {"type": "string", "description": "what it gets, e.g. uid=0(root)"},
				"verified":     {"type": "boolean"},
			}, "secret_value"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				c, err := d.AddCredential(str(a, "engagement"), CredentialIn{
					ID: str(a, "id"), Target: str(a, "target"), User: str(a, "user"),
					SecretKind: str(a, "secret_kind"), SecretValue: str(a, "secret_value"),
					Source: str(a, "source"), SecretRef: str(a, "secret_ref"),
					Privilege: str(a, "privilege"), Verified: boolArg(a, "verified"),
				})
				return jsonOf(c), err
			}},

		{Name: "redteam_credential_list", Title: "列凭据",
			Desc: "List the engagement's credentials. Secrets are masked unless with_secrets is true.",
			Schema: obj(objProps{
				"engagement":   {"type": "string"},
				"with_secrets": {"type": "boolean", "description": "show the secret itself (default false)"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				with := false
				if v, ok := a["with_secrets"].(bool); ok {
					with = v
				}
				list, err := d.Credentials(str(a, "engagement"), with)
				return jsonOf(list), err
			}},

		{Name: "redteam_access_add", Title: "记录入口",
			Desc: "Record an access gained: how, as whom, and on what (a login, a reverse shell, a forwarding session).",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string", "description": "host:port that was entered"},
				"user":       {"type": "string"},
				"password":   {"type": "string"},
				"method":     {"type": "string", "description": "web-login | reverse-shell | ssh | sql | pth | rdp"},
				"listen":     {"type": "string", "description": "the listener the shell came back to"},
				"privilege":  {"type": "string"},
				"session":    {"type": "string", "description": "the session's name, if it has one"},
			}), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				e, err := d.AddEntry(str(a, "engagement"), EntryIn{
					Target: str(a, "target"), User: str(a, "user"), Password: str(a, "password"),
					Method: str(a, "method"), Listen: str(a, "listen"), Privilege: str(a, "privilege"),
					Session: str(a, "session"),
				})
				return jsonOf(e), err
			}},

		{Name: "redteam_access_list", Title: "列入口",
			Desc: "List the accesses gained in this engagement.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.Entries(str(a, "engagement"))
				return jsonOf(list), err
			}},

		{Name: "redteam_score_hit", Title: "记分",
			Desc: "Claim points for a hit. One score per category and target: a host taken twice is worth its points once. The target must carry its port.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"category":   {"type": "string", "description": "server-host | web-app | db-credential | central-system"},
				"target":     {"type": "string", "description": "host:port, always with the port"},
				"points":     {"type": "integer", "description": "the points the rulebook gives it"},
				"evidence":   {"type": "string", "description": "what was gained, e.g. 10.0.0.5:22｜root shell（uid=0）"},
			}, "category", "target", "points"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				s, err := d.Score(str(a, "engagement"), str(a, "category"), str(a, "target"),
					intArg(a, "points"), str(a, "evidence"))
				return jsonOf(s), err
			}},

		{Name: "redteam_tunnel_add", Title: "记录隧道",
			Desc: "Record a tunnel into a network, and which end runs on the target. Registering the same listen address again amends it (a re-check is an amend with status and reachable).",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"id":         {"type": "string", "description": "the tunnel's id, to amend it"},
				"kind":       {"type": "string", "description": "socks5 | port-forward"},
				"entry_kind": {"type": "string", "description": "target-outbound (the target dials out) | target-http (through a webshell) | target-agent (through a process already owned)"},
				"listen":     {"type": "string", "description": "the local end, e.g. 127.0.0.1:1080"},
				"via":        {"type": "string", "description": "the asset or webshell it runs through"},
				"reachable":  {"type": "string", "description": "the networks measured through it"},
				"status":     {"type": "string", "enum": []string{"up", "down"}},
			}), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				t, err := d.AddTunnel(str(a, "engagement"), TunnelIn{
					ID: str(a, "id"), Kind: str(a, "kind"), EntryKind: str(a, "entry_kind"),
					Listen: str(a, "listen"), Via: str(a, "via"), Reachable: str(a, "reachable"),
					Status: str(a, "status"),
				})
				return jsonOf(t), err
			}},

		{Name: "redteam_tunnel_update", Title: "更新隧道",
			Desc: "Amend a tunnel by its id: its status after a check, and the networks it now reaches.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"id":         {"type": "string"},
				"status":     {"type": "string", "enum": []string{"up", "down"}},
				"reachable":  {"type": "string"},
				"via":        {"type": "string"},
			}, "id"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				t, err := d.AddTunnel(str(a, "engagement"), TunnelIn{
					ID: str(a, "id"), Status: str(a, "status"), Reachable: str(a, "reachable"), Via: str(a, "via"),
				})
				return jsonOf(t), err
			}},

		{Name: "redteam_webshell_add", Title: "记录 WebShell",
			Desc: "Record a webshell. verified is for a shell the operator really connected to: write it only after that.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string", "description": "host:port the shell is on"},
				"url":        {"type": "string"},
				"shell_type": {"type": "string", "description": "behinder | godzilla | antsword | custom"},
				"pass_key":   {"type": "string"},
				"privilege":  {"type": "string"},
				"status":     {"type": "string", "enum": []string{"online", "offline", "dead"}, "description": "the last re-check; empty is never re-checked"},
				"verified":   {"type": "boolean"},
			}, "url"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				w, err := d.AddWebshell(str(a, "engagement"), WebshellIn{
					Target: str(a, "target"), URL: str(a, "url"), ShellType: str(a, "shell_type"),
					PassKey: str(a, "pass_key"), Privilege: str(a, "privilege"), Status: str(a, "status"),
					Verified: boolArg(a, "verified"),
				})
				return jsonOf(w), err
			}},

		{Name: "redteam_webshell_update", Title: "更新 WebShell",
			Desc: "Amend a shell by its URL: the last re-check's outcome (online / offline / dead), what it runs as, whether the operator has really connected. A dead shell is left as it is — the next role reads this before trusting one.",
			Schema: obj(objProps{
				"url":       {"type": "string", "description": "the shell's URL (the record's key)"},
				"status":    {"type": "string", "enum": []string{"online", "offline", "dead"}},
				"privilege": {"type": "string"},
				"verified":  {"type": "boolean", "description": "the operator connected to it for real"},
			}, "url"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				w, err := d.AddWebshell(str(a, "engagement"), WebshellIn{
					URL: str(a, "url"), Privilege: str(a, "privilege"), Status: str(a, "status"),
					Verified: boolArg(a, "verified"),
				})
				return jsonOf(w), err
			}},

		{Name: "redteam_session_check", Title: "登记会话",
			Desc: "Register a session and record that it was just checked: a shell, a forward, a database connection. The same name again is a re-check.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string"},
				"name":       {"type": "string", "description": "the session's name (default: one made for it)"},
				"how":        {"type": "string", "description": "how it is reached, e.g. ssh root@10.0.0.5"},
			}, "target"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				s, err := d.AddSession(str(a, "engagement"), str(a, "target"), str(a, "name"), str(a, "how"))
				return jsonOf(s), err
			}},

		{Name: "redteam_chain_add", Title: "记录动作",
			Desc: "Record one action: the command exactly as it was run, and a summary of what came back. Every step of the engagement gets one.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"stage_code": {"type": "string", "description": "recon | internet | boundary | internal"},
				"role":       {"type": "string", "description": "the role that ran it"},
				"tool":       {"type": "string", "description": "the command, verbatim"},
				"result":     {"type": "string", "description": "the output's summary"},
			}), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				c, err := d.AddChain(str(a, "engagement"), str(a, "stage_code"), str(a, "role"),
					str(a, "tool"), str(a, "result"))
				return jsonOf(c), err
			}},

		{Name: "redteam_http_evidence_add", Title: "记录证据",
			Desc: "Keep the raw request and the response it got: the proof a finding rests on.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"target":     {"type": "string"},
				"kind":       {"type": "string", "description": "http | command | file"},
				"request":    {"type": "string"},
				"response":   {"type": "string"},
			}), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				e, err := d.AddEvidence(str(a, "engagement"), str(a, "target"), str(a, "kind"),
					str(a, "request"), str(a, "response"))
				return jsonOf(e), err
			}},

		{Name: "redteam_attack_file_add", Title: "记录载荷文件",
			Desc: "Record a file built for the engagement (a payload, a shell, a dump), where it is, and which asset it went to.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
				"name":       {"type": "string"},
				"path":       {"type": "string", "description": "where the file is on this machine"},
				"purpose":    {"type": "string"},
				"delivered":  {"type": "string", "description": "the asset it was put on"},
			}, "name"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				f, err := d.AddAttackFile(str(a, "engagement"), str(a, "name"), str(a, "path"),
					str(a, "purpose"), str(a, "delivered"))
				return jsonOf(f), err
			}},

		{Name: "redteam_poc_add", Title: "记录 POC",
			Desc: "Keep a technique that worked, under a name, for the next engagement. The same name again amends it (a second engagement verifying it adds a note, not a row). Redact the target's details.",
			Schema: obj(objProps{
				"engagement":    {"type": "string"},
				"name":          {"type": "string"},
				"category":      {"type": "string", "description": "unauth | nday | weak-cred | config | dos…"},
				"asset_target":  {"type": "string", "description": "what it was verified against (redact the unit)"},
				"severity":      {"type": "string"},
				"content":       {"type": "string", "description": "the script or request itself"},
				"verified_note": {"type": "string", "description": "how it was shown to work"},
				"verified":      {"type": "boolean"},
			}, "name"), Mutates: true, Run: func(d *DB, a map[string]any) (string, error) {
				p, err := d.AddPoc(PocIn{
					Name: str(a, "name"), Category: str(a, "category"), Engagement: str(a, "engagement"),
					AssetTarget: str(a, "asset_target"), Severity: str(a, "severity"),
					Content: str(a, "content"), VerifiedNote: str(a, "verified_note"),
					Verified: boolArg(a, "verified"),
				})
				return jsonOf(p), err
			}},

		{Name: "redteam_poc_search", Title: "查 POC",
			Desc: "Search every engagement's techniques by a word in their name, category or content. Search here before writing an exploit from scratch.",
			Schema: obj(objProps{
				"query": {"type": "string", "description": "redis, log4j, tomcat…"},
			}, "query"), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.SearchPoc("", str(a, "query"))
				return jsonOf(list), err
			}},

		{Name: "redteam_poc_get", Title: "取 POC",
			Desc: "Read one technique by its name.",
			Schema: obj(objProps{
				"name": {"type": "string"},
			}, "name"), Run: func(d *DB, a map[string]any) (string, error) {
				p, err := d.PocByName(str(a, "name"))
				return jsonOf(p), err
			}},

		{Name: "redteam_report", Title: "演练汇总",
			Desc: "The engagement rolled up: what was found, what was scored, and every list the commander checks a subagent's report against. Read this before claiming progress.",
			Schema: obj(objProps{
				"engagement": {"type": "string"},
			}),
			Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.Report(str(a, "engagement"))
				if err != nil {
					return "", err
				}
				return jsonOf(r), nil
			}},

		{Name: "redteam_asset_get", Title: "取资产",
			Desc: "One asset by its id, or by its host / host:port — with everything a rescan appended (test, surface, tags, names).",
			Schema: obj(objProps{
				"id":    {"type": "string"},
				"query": {"type": "string", "description": "host or host:port, when no id is given"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.AssetGet(str(a, "engagement"), str(a, "id"), str(a, "query"))
				return jsonOf(r), err
			}},

		{Name: "redteam_asset_stats", Title: "资产统计",
			Desc:   "The engagement's assets counted: total, per scope, per status, per priority, and the /24s with hosts in them. The collection phase's closing check.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.AssetStats(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_asset_timeline", Title: "资产时间线",
			Desc: "Assets by the day they were found (internal and internet apart), and the latest ones. Answers 'when did we find this' and shows a gap in collection.",
			Schema: obj(objProps{
				"limit": {"type": "number", "description": "how many of the latest to list (default 50)"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.AssetTimeline(str(a, "engagement"), intArg(a, "limit"))
				return jsonOf(r), err
			}},

		{Name: "redteam_asset_graph", Title: "资产图谱",
			Desc:   "The engagement as a graph: segments, the assets in each, and every edge (contains, resolves-to, and what redteam_asset_link added). For planning lateral movement.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.AssetGraph(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_chain", Title: "动作清单",
			Desc: "The engagement's recorded actions (stage, role, tool, result), oldest last. Read this to see what was already tried before dispatching a role.",
			Schema: obj(objProps{
				"limit": {"type": "number", "description": "the last N actions (default 50)"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.ChainList(str(a, "engagement"), intArg(a, "limit"))
				return jsonOf(r), err
			}},

		{Name: "redteam_tunnel_list", Title: "隧道清单",
			Desc:   "Every tunnel: kind, which end is on the target, the listen address, and the networks it reaches. Only an up one with reachable filled is a path inside.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.Tunnels(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_webshell_list", Title: "WebShell 清单",
			Desc:   "Every shell recorded: its URL, type, pass key and whether the operator has actually connected. Only a verified one is an entry.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.Webshells(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_vuln_query", Title: "查漏洞",
			Desc: "The engagement's vulnerabilities, or the ones on one target. Check here before testing a host again.",
			Schema: obj(objProps{
				"target": {"type": "string", "description": "only this host:port's, when given"},
			}), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.Vulns(str(a, "engagement"))
				if err != nil {
					return "", err
				}
				if t := str(a, "target"); t != "" {
					kept := list[:0]
					for _, v := range list {
						if v.Target == t {
							kept = append(kept, v)
						}
					}
					list = kept
				}
				return jsonOf(list), nil
			}},

		{Name: "redteam_poc_list", Title: "POC 清单",
			Desc:   "Every technique kept across all engagements, newest first. Skim this before writing an exploit.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				list, err := d.Pocs()
				return jsonOf(list), err
			}},

		{Name: "redteam_attack_file_list", Title: "载荷清单",
			Desc:   "Every file built or delivered in this engagement: name, where it is, what it was for, where it went.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.AttackFiles(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_score_list", Title: "得分面板",
			Desc:   "The engagement's scored targets grouped by category, with the total. Read this before planning the next move: the categories with nothing yet are where the remaining points are.",
			Schema: obj(objProps{}), Run: func(d *DB, a map[string]any) (string, error) {
				r, err := d.ScoreList(str(a, "engagement"))
				return jsonOf(r), err
			}},

		{Name: "redteam_engagement_open", Title: "打开演练",
			Desc: "Return the engagement for a target without making one: what is already recorded there. redteam_engagement_begin is the one that creates.",
			Schema: obj(objProps{
				"target": {"type": "string", "description": "the unit's name"},
			}, "target"), Run: func(d *DB, a map[string]any) (string, error) {
				eng, err := d.Open(str(a, "target"))
				if err != nil {
					return "", err
				}
				return jsonOf(eng), nil
			}},

		{Name: "redteam_engagements", Title: "演练列表",
			Desc:   "List every engagement, newest first.",
			Schema: obj(objProps{}),
			Run: func(d *DB, _ map[string]any) (string, error) {
				list, err := d.Engagements()
				return jsonOf(list), err
			}},
	}
}

// ToolNamed is the tool by its name.
func ToolNamed(name string) (Tool, bool) {
	for _, t := range Tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// ---------------------------------------------------------------------
// argument helpers
// ---------------------------------------------------------------------

type objProps map[string]map[string]any

// obj is a JSON Schema object with props and required names.
func obj(props objProps, required ...string) string {
	props["engagement"] = map[string]any{"type": "string", "description": "the engagement or its target; empty is the newest"}
	// a stable order in the schema, so tools/list reads the same twice
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`{"type":"object","additionalProperties":false,"properties":{`)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		v, _ := json.Marshal(props[k])
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteString(":")
		b.Write(v)
	}
	b.WriteString(`}`)
	if len(required) > 0 {
		rb, _ := json.Marshal(required)
		b.WriteString(`,"required":`)
		b.Write(rb)
	}
	b.WriteString(`}`)
	return b.String()
}

func str(a map[string]any, key string) string {
	if v, ok := a[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func intArg(a map[string]any, key string) int {
	switch v := a[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func boolArg(a map[string]any, key string) *bool {
	if v, ok := a[key].(bool); ok {
		return &v
	}
	return nil
}

// jsonOf is a value as the tool's answer, one line where it can be.
func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("{\"error\":%q}", err.Error())
	}
	return string(b)
}

// assetTable is an engagement's assets as a compact table a model reads
// more cheaply than the same rows as JSON.
func assetTable(list []Asset) string {
	if len(list) == 0 {
		return "no asset"
	}
	var b strings.Builder
	b.WriteString("ip:port\tservice\ttitle\tstatus\tpriority\tscope\ttags\n")
	for _, a := range list {
		addr := a.IP
		if a.Port != 0 {
			addr = net.JoinHostPort(a.IP, strconv.Itoa(a.Port))
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", addr, a.Service, cut(a.Title, 40), a.Status, a.Priority, a.Scope, a.Tags)
	}
	return b.String()
}

// cut is s at most n runes.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
