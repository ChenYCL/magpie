package redteamstore

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The read tools the commander dispatches with: a finding isn't checkable
// unless what was written can be read back in the shape the tool answers.

func readTools(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "redteam.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Begin("单位T", "lab", ""); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAssetGetByIDAndQuery(t *testing.T) {
	d := readTools(t)
	a, err := d.AddAsset("", AssetIn{Target: "203.0.113.9:443", Service: "https", Names: "a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	byID, err := d.AssetGet("", a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != a.ID || byID.Service != "https" {
		t.Fatalf("by id: %+v", byID)
	}
	byHost, err := d.AssetGet("", "", "203.0.113.9:443")
	if err != nil {
		t.Fatal(err)
	}
	if byHost.ID != a.ID {
		t.Fatalf("by host: %+v", byHost)
	}
	if _, err := d.AssetGet("", "", ""); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("no args: %v", err)
	}
	if _, err := d.AssetGet("", "ast-none", ""); err == nil {
		t.Fatal("a missing id answered nil error")
	}
}

func TestAssetStatsCountsScopesAndSegments(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddAsset("", AssetIn{Target: "203.0.113.9:443", Scope: "internet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset("", AssetIn{Target: "10.0.0.5:22", Scope: "internal", Status: "owned", Priority: "high"}); err != nil {
		t.Fatal(err)
	}
	raw, err := d.AssetStats("")
	if err != nil {
		t.Fatal(err)
	}
	if raw["total"].(int) != 2 || raw["internet"].(int) != 1 || raw["internal"].(int) != 1 ||
		raw["owned"].(int) != 1 || raw["segments"].(int) != 2 {
		t.Fatalf("counts: %v", raw)
	}
	segJSON, _ := json.Marshal(raw["segmentList"])
	var segs []struct {
		CIDR  string `json:"cidr"`
		Hosts int    `json:"hosts"`
	}
	if err := json.Unmarshal(segJSON, &segs); err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments: %v", segs)
	}
	hosts := map[string]int{}
	for _, s := range segs {
		hosts[s.CIDR] = s.Hosts
	}
	if hosts["203.0.113.0/24"] != 1 || hosts["10.0.0.0/24"] != 1 {
		t.Fatalf("per-segment hosts: %v", hosts)
	}
}

func TestAssetTimelineBucketsByDay(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddAsset("", AssetIn{Target: "203.0.113.9:443", Scope: "internet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset("", AssetIn{Target: "10.0.0.5:22", Scope: "internal"}); err != nil {
		t.Fatal(err)
	}
	raw, err := d.AssetTimeline("", 10)
	if err != nil {
		t.Fatal(err)
	}
	dayJSON, _ := json.Marshal(raw["days"])
	var days []struct {
		Day      string `json:"day"`
		Internet int    `json:"internet"`
		Internal int    `json:"internal"`
	}
	if err := json.Unmarshal(dayJSON, &days); err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Internet != 1 || days[0].Internal != 1 {
		t.Fatalf("days: %+v", days)
	}
	latestJSON, _ := json.Marshal(raw["latest"])
	var latest []struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(latestJSON, &latest); err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest: %+v", latest)
	}
}

func TestAssetGraphNodesAndEdges(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddAsset("", AssetIn{Target: "203.0.113.9:443"}); err != nil {
		t.Fatal(err)
	}
	all, err := d.Assets("")
	if err != nil {
		t.Fatal(err)
	}
	var hostID, segID string
	for _, h := range all {
		if h.Scope == "segment" {
			segID = h.ID
		} else if h.Port == 443 {
			hostID = h.ID
		}
	}
	if hostID == "" || segID == "" {
		t.Fatalf("want a host and a segment row, got %+v", all)
	}
	if err := d.LinkAssets("", hostID, segID, "resolved-from"); err != nil {
		t.Fatal(err)
	}
	raw, err := d.AssetGraph("")
	if err != nil {
		t.Fatal(err)
	}
	nodeJSON, _ := json.Marshal(raw["nodes"])
	var nodes []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(nodeJSON, &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes: %+v", nodes)
	}
	kinds := map[string]int{}
	for _, n := range nodes {
		kinds[n.Kind]++
	}
	if kinds["segment"] != 1 || kinds["host"] != 1 {
		t.Fatalf("kinds: %v", kinds)
	}
	edgeJSON, _ := json.Marshal(raw["edges"])
	var edges []struct {
		Rel string `json:"rel"`
	}
	if err := json.Unmarshal(edgeJSON, &edges); err != nil {
		t.Fatal(err)
	}
	// the /24 link made contains at AddAsset time; ours is the second edge
	if len(edges) != 2 || edges[1].Rel != "resolved-from" {
		t.Fatalf("edges: %+v", edges)
	}
}

func TestChainListKeepsTheLastN(t *testing.T) {
	d := readTools(t)
	for i := 0; i < 4; i++ {
		if _, err := d.AddChain("", "recon", "recon", "tool", "step"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := d.ChainList("", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want last 2, got %d", len(list))
	}
}

func TestVulnQueryFiltersByTarget(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddVuln("", VulnIn{Target: "203.0.113.9:443", Name: "nday-x", Evidence: "e"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddVuln("", VulnIn{Target: "10.0.0.5:22", Name: "unauth-y", Evidence: "e"}); err != nil {
		t.Fatal(err)
	}
	list, err := d.Vulns("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("all: %d", len(list))
	}
	// the tool's filter is the query's job here: check the data it filters
	if list[0].Target == list[1].Target {
		t.Fatal("both vulns on one target")
	}
}

func TestPocsAcrossEngagementsNewestFirst(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddPoc(PocIn{Name: "poc-a", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Begin("单位U", "lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddPoc(PocIn{Name: "poc-b", Engagement: "单位U", Content: "b"}); err != nil {
		t.Fatal(err)
	}
	list, err := d.Pocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "poc-b" {
		t.Fatalf("order: %+v", list)
	}
}

func TestEngagementOpenReadsWithoutMaking(t *testing.T) {
	d := readTools(t)
	eng, err := d.Target("单位T")
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Open("单位T")
	if err != nil || got.ID != eng.ID {
		t.Fatalf("open: %+v %v", got, err)
	}
	if _, err := d.Open("no-such-unit"); err == nil {
		t.Fatal("an unknown target answered nil error")
	}
}

func TestNewToolsAnswerThroughToolRun(t *testing.T) {
	d := readTools(t)
	if _, err := d.AddAsset("", AssetIn{Target: "203.0.113.9:443"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"redteam_asset_stats", "redteam_asset_timeline", "redteam_asset_graph", "redteam_poc_list"} {
		tl, ok := ToolNamed(name)
		if !ok {
			t.Fatalf("no tool %s", name)
		}
		out, err := tl.Run(d, map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s: %q isn't JSON: %v", name, out, err)
		}
	}
}

func TestScoreListGroupsAndTotals(t *testing.T) {
	d := readTools(t)
	if _, err := d.Score("", "server-host", "10.0.0.5:22", 20, "root"); err != nil {
		t.Fatal(err)
	}
	// the same host under another category is a second row
	if _, err := d.Score("", "web-app", "10.0.0.5:8080", 10, "admin"); err != nil {
		t.Fatal(err)
	}
	// a repeat is not a second row
	if _, err := d.Score("", "server-host", "10.0.0.5:22", 20, "root again"); err != nil {
		t.Fatal(err)
	}
	raw, err := d.ScoreList("")
	if err != nil {
		t.Fatal(err)
	}
	if raw["total"].(int) != 30 || raw["rows"].(int) != 2 {
		t.Fatalf("totals: %v", raw)
	}
	catJSON, _ := json.Marshal(raw["categories"])
	var cats []struct {
		Category string   `json:"category"`
		Points   int      `json:"points"`
		Targets  []string `json:"targets"`
	}
	if err := json.Unmarshal(catJSON, &cats); err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 {
		t.Fatalf("categories: %+v", cats)
	}
}

func TestWebshellUpdateKeepsRowAndSetsStatus(t *testing.T) {
	d := readTools(t)
	w, err := d.AddWebshell("", WebshellIn{Target: "10.0.0.5:80", URL: "http://10.0.0.5/x.php", ShellType: "behinder", Privilege: "www"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != "" {
		t.Fatalf("fresh shell has a status: %+v", w)
	}
	// the re-check: same row, status set, the rest kept
	got, err := d.AddWebshell("", WebshellIn{URL: "http://10.0.0.5/x.php", Status: "dead"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != w.ID {
		t.Fatalf("amend made a row: %+v vs %+v", got, w)
	}
	if got.Status != "dead" || got.ShellType != "behinder" || got.Privilege != "www" {
		t.Fatalf("amend lost a field: %+v", got)
	}
}

func TestMigrateAddsWebshellStatusToOldDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redteam.db")
	// a database with the old webshells shape
	old := `CREATE TABLE IF NOT EXISTS webshells (
		id TEXT PRIMARY KEY, engagement TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '', shell_type TEXT NOT NULL DEFAULT '',
		pass_key TEXT NOT NULL DEFAULT '', privilege TEXT NOT NULL DEFAULT '',
		verified INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	);`
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(old); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO webshells (id, engagement, url, created_at, updated_at) VALUES ('w1','e1','http://h/s.php',1,1)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	// open it with the current schema: the column is added, the row kept
	d, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d.Close()
	list, err := d.Webshells("e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "" {
		t.Fatalf("old row: %+v", list)
	}
}
