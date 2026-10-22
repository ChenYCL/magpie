package redteamstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTest opens a store in a folder of the test's own.
func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "redteam.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// A store that can't be opened is an error, never an empty store: a report
// that shows nothing because the file is broken would read as "nothing was
// found" (code standards: a failed read means unknown).
func TestUnreadableStoreIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redteam.db")
	if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err == nil {
		d.Close()
		t.Fatal("Open took a file that isn't a database")
	}
	if !strings.Contains(err.Error(), "redteam store schema") && !strings.Contains(err.Error(), "database") {
		t.Fatalf("Open failed with %v, which doesn't say the store couldn't be read", err)
	}
}

// A second Open of the same file reads what the first wrote: the store is
// on disk, not in the process (a restart keeps every finding).
func TestStoreSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redteam.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := first.Begin("单位A", "授权范围：10.0.0.0/8", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddAsset(eng.ID, AssetIn{Target: "10.0.0.5:22", Service: "ssh", Tool: "fscan"}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	assets, err := second.AssetQuery(eng.ID, "10.0.0.5:22")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].Service != "ssh" {
		t.Fatalf("after reopen: %+v", assets)
	}
}

// The same target named twice is one engagement: the skills name the unit
// on every call, and two rows would split its assets.
func TestBeginIsIdempotent(t *testing.T) {
	d := openTest(t)
	a, err := d.Begin("单位B", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Begin("单位B", "another scope", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("two ids for one target: %s, %s", a.ID, b.ID)
	}
	list, err := d.Engagements()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("engagements: %d, want 1", len(list))
	}
}

// A tool call naming the target (not the id) finds the engagement begun
// for it; one naming nothing is the newest engagement.
func TestTargetByTargetNameAndNewest(t *testing.T) {
	d := openTest(t)
	begun, err := d.Begin("单位C", "", "")
	if err != nil {
		t.Fatal(err)
	}
	byName, err := d.Target("单位C")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != begun.ID {
		t.Fatalf("Target(单位C) = %s, want %s", byName.ID, begun.ID)
	}
	newest, err := d.Target("")
	if err != nil {
		t.Fatal(err)
	}
	if newest.ID != begun.ID {
		t.Fatalf("Target(empty) = %s, want %s", newest.ID, begun.ID)
	}
	if _, err := d.Target(""); err != nil && lastErrNoEngagement(err) {
		t.Fatalf("newest engagement not found: %v", err)
	}
}

func lastErrNoEngagement(err error) bool { return strings.Contains(err.Error(), "no engagement yet") }

// No engagement at all is an error naming the tool to call first, not an
// empty list.
func TestTargetWithNoEngagementSaysWhatToDo(t *testing.T) {
	d := openTest(t)
	_, err := d.Target("")
	if err == nil || !strings.Contains(err.Error(), "redteam_engagement_begin") {
		t.Fatalf("Target(\"\") = %v, want an error naming redteam_engagement_begin", err)
	}
}

// Adding an asset again amends it: what the first call found stays, what
// the second adds is appended, and the row count doesn't grow.
func TestAddAssetAmendsRatherThanReplaces(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位D", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "203.0.113.9:80", Service: "http",
		Title: "nginx", Test: "curl -I → 200", Tags: "edge"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.AddAsset(eng.ID, AssetIn{Target: "203.0.113.9:80",
		Test: "dirsearch → /admin 401", Surface: "/admin", Status: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Service != "http" || got.Title != "nginx" || got.Tags != "edge" {
		t.Fatalf("the second call dropped what the first found: %+v", got)
	}
	if !strings.Contains(got.Test, "curl -I → 200") || !strings.Contains(got.Test, "dirsearch") {
		t.Fatalf("test isn't both attempts appended: %q", got.Test)
	}
	if got.Surface != "/admin" || got.Status != "testing" {
		t.Fatalf("the second call's surface/status missing: %q %q", got.Surface, got.Status)
	}
	list, err := d.Assets(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hosts int
	for _, a := range list {
		if a.IP == "203.0.113.9" {
			hosts++
		}
	}
	if hosts != 1 {
		t.Fatalf("203.0.113.9 has %d rows, want 1", hosts)
	}
}

// An asset whose address is in a /24 gets that /24 as an asset of its own,
// linked by "contains" — the skills' 按 /24 自动建 C 段.
func TestAddAssetLinksItsSegmentOnce(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位E", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"198.51.100.7:443", "198.51.100.8:443"} {
		if _, err := d.AddAsset(eng.ID, AssetIn{Target: host, Scope: "internal"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := d.Assets(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	segments := 0
	for _, a := range list {
		if a.Tags == "segment" && a.IP == "198.51.100.0/24" {
			segments++
		}
	}
	if segments != 1 {
		t.Fatalf("the /24 has %d rows, want 1: %+v", segments, list)
	}
	edges, err := d.Edges(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	var contains int
	for _, e := range edges {
		if e.Rel == "contains" {
			contains++
		}
	}
	if contains != 2 {
		t.Fatalf("%d contains edges for two addresses, want 2", contains)
	}
}

// A host that isn't an address (a domain) gets no segment: there is no
// /24 to make of it.
func TestDomainAssetGetsNoSegment(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位F", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "shop.example.com:443", Names: "shop.example.com"}); err != nil {
		t.Fatal(err)
	}
	edges, err := d.Edges(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("a domain got an edge: %+v", edges)
	}
}

// The same host and port on the two sides of the boundary are two assets:
// 10.0.0.5 reached from outside and from inside is not one finding.
func TestScopeKeepsInternetAndInternalApart(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位G", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "10.0.0.5:80", Scope: "internet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "10.0.0.5:80", Scope: "internal"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.AssetQuery(eng.ID, "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d rows for one address on two sides, want 2", len(got))
	}
}

// A score for the same category and target is written once: a host taken
// twice is worth its points once, as the skills' "同一资产同一端口只算一次分".
func TestScoreIsCountedOncePerTargetAndCategory(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位H", "", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Score(eng.ID, "server-host", "10.0.0.5:22", 20, "root shell")
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.Score(eng.ID, "server-host", "10.0.0.5:22", 20, "root shell again")
	if err != nil {
		t.Fatal(err)
	}
	if first.Already {
		t.Fatal("the first hit says it was already counted")
	}
	if !again.Already || again.ID != first.ID {
		t.Fatalf("a second hit of the same target counted again: %+v", again)
	}
	// another category is another score
	if other, err := d.Score(eng.ID, "web-app", "10.0.0.5:22", 10, "admin panel"); err != nil || other.Already {
		t.Fatalf("a score of another category was refused or deduplicated: %+v %v", other, err)
	}
	scores, err := d.Scores(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for _, s := range scores {
		total += s.Points
	}
	if total != 30 || len(scores) != 2 {
		t.Fatalf("scores %+v total %d, want 2 rows of 20 and 10", scores, total)
	}
}

// A credential is kept as given (its plaintext is what an agent reuses),
// and amending it by id keeps what wasn't sent.
func TestCredentialKeepsWhatIsNotSent(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位I", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cr, err := d.AddCredential(eng.ID, CredentialIn{Target: "10.0.0.5:22", User: "root",
		SecretValue: "Root@123", Source: "hydra", SecretRef: "runs/hydra.txt"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.AddCredential(eng.ID, CredentialIn{ID: cr.ID, Verified: new(true), Privilege: "uid=0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SecretValue != "Root@123" || got.User != "root" || got.SecretRef != "runs/hydra.txt" {
		t.Fatalf("the amend dropped the secret: %+v", got)
	}
	if !got.Verified || got.Privilege != "uid=0" {
		t.Fatalf("the amend didn't land: %+v", got)
	}
	if _, err := d.AddCredential(eng.ID, CredentialIn{ID: "nope", Verified: new(true)}); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("amending a missing credential: %v, want ErrNoRecord", err)
	}
}

// A vulnerability is amended in place: its status moves from open to
// exploited, and the evidence it had stays.
func TestVulnAmendKeepsEvidence(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位J", "", "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.AddVuln(eng.ID, VulnIn{Target: "10.0.0.5:6379", Name: "Redis 未授权", Severity: "critical",
		Evidence: "INFO 回显", Kind: "unauth", Confidence: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.AddVuln(eng.ID, VulnIn{ID: v.ID, Status: "exploited", Gained: "数据库权限"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Redis 未授权" || got.Evidence != "INFO 回显" {
		t.Fatalf("the amend dropped what the first call found: %+v", got)
	}
	if got.Status != "exploited" || got.Gained != "数据库权限" {
		t.Fatalf("the amend didn't land: %+v", got)
	}
}

// The report rolls an engagement up: what was found, what was scored, and
// the counts the commander checks a subagent's claim against.
func TestReportCountsEverything(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位K", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "192.0.2.10:80", Service: "http"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAsset(eng.ID, AssetIn{Target: "10.20.30.11:22", Service: "ssh", Scope: "internal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddVuln(eng.ID, VulnIn{Target: "192.0.2.10:80", Name: "x", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Score(eng.ID, "server-host", "10.20.30.11:22", 20, "root"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddChain(eng.ID, "recon", "recon", "subfinder -d example.com", "42 子域"); err != nil {
		t.Fatal(err)
	}
	r, err := d.Report(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	// two hosts in two /24s, and the /24 each is in
	if r.Assets != 4 {
		t.Fatalf("assets %d, want 4 (two hosts + a /24 each)", r.Assets)
	}
	if r.Internet != 1 || r.Internal != 1 {
		t.Fatalf("sides: %d internet, %d internal; want 1 and 1 (the /24s are neither)", r.Internet, r.Internal)
	}
	if r.Vulns != 1 || r.Scores != 20 || r.Chains != 1 {
		t.Fatalf("report %+v", r)
	}
	if r.Engagement.ID != eng.ID {
		t.Fatalf("report names %s, want %s", r.Engagement.ID, eng.ID)
	}
}

// Everything a tool writes is readable by the next call of another tool:
// a tunnel's last check, a session, a shell.
func TestEntriesAreListedBack(t *testing.T) {
	d := openTest(t)
	eng, err := d.Begin("单位L", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddTunnel(eng.ID, TunnelIn{Kind: "socks5", EntryKind: "target-http",
		Listen: "127.0.0.1:1080", Via: "10.0.0.5:8080", Reachable: "10.10.0.0/16", Status: "up"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddWebshell(eng.ID, WebshellIn{Target: "10.0.0.5:80", URL: "http://10.0.0.5/s.jsp",
		ShellType: "behinder", PassKey: "k", Verified: new(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddEntry(eng.ID, EntryIn{Target: "10.0.0.5:22", User: "root", Method: "ssh",
		Privilege: "uid=0", Session: "ssh-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddSession(eng.ID, "10.0.0.5:22", "sess-1", "root shell"); err != nil {
		t.Fatal(err)
	}
	tunnels, err := d.Tunnels(eng.ID)
	if err != nil || len(tunnels) != 1 || tunnels[0].Status != "up" {
		t.Fatalf("tunnels %+v %v", tunnels, err)
	}
	shells, err := d.Webshells(eng.ID)
	if err != nil || len(shells) != 1 || !shells[0].Verified {
		t.Fatalf("webshells %+v %v", shells, err)
	}
	entries, err := d.Entries(eng.ID)
	if err != nil || len(entries) != 1 || entries[0].Session != "ssh-1" {
		t.Fatalf("entries %+v %v", entries, err)
	}
	sessions, err := d.Sessions(eng.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Status != "live" {
		t.Fatalf("sessions %+v %v", sessions, err)
	}
}

// A POC verified in a later engagement keeps its first one: the knowledge
// base outlives the engagement it was learned in.
func TestPocIsKeptAcrossEngagements(t *testing.T) {
	d := openTest(t)
	first, err := d.Begin("单位M", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddPoc(PocIn{Name: "redis-unauth-write", Category: "unauth", Engagement: first.ID,
		Content: "redis-cli -h {host} config set dir /var/www", Verified: new(true), VerifiedNote: "写马成功"}); err != nil {
		t.Fatal(err)
	}
	second, err := d.Begin("单位N", "", "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := d.SearchPoc(first.ID, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("a POC from another engagement isn't found: %+v", list)
	}
	got, err := d.PocByName("redis-unauth-write")
	if err != nil {
		t.Fatalf("PocByName: %v", err)
	}
	if got.Content == "" || !got.Verified {
		t.Fatalf("POC by name: %+v", got)
	}
	if _, err := d.AddPoc(PocIn{Name: "redis-unauth-write", Category: "unauth", Engagement: second.ID}); err != nil {
		t.Fatalf("re-verifying a known POC in another engagement: %v", err)
	}
}

// deleteAll is how a test empties the store if it needs to; it is not
// exported, so no tool can drop a finding.
func deleteAll(t *testing.T, d *DB) {
	t.Helper()
	err := d.do(func(conn *sql.DB) error {
		for _, table := range []string{"engagements", "assets", "asset_edges", "vulns", "credentials",
			"services_entries", "tunnels", "webshells", "pocs", "scores", "chains", "evidence", "attack_files"} {
			if _, err := conn.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
