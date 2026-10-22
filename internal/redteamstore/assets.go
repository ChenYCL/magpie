package redteamstore

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Engagement is one authorized target, as an operator named it.
type Engagement struct {
	ID        string `json:"id"`
	Target    string `json:"target"`
	Scope     string `json:"scope,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Begin writes an engagement, or returns the one already at that target.
// A target named twice keeps the first engagement's id: the skills name a
// target by its unit name every time, and a second row would split the
// assets of one engagement in two.
func (d *DB) Begin(target, scope, notes string) (Engagement, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return Engagement{}, errors.New("an engagement needs a target")
	}
	var out Engagement
	err := d.do(func(conn *sql.DB) error {
		row := conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements WHERE target = ? ORDER BY created_at LIMIT 1`, target)
		err := row.Scan(&out.ID, &out.Target, &out.Scope, &out.Notes, &out.CreatedAt, &out.UpdatedAt)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := Now()
		out = Engagement{ID: newID("eng"), Target: target, Scope: scope, Notes: notes, CreatedAt: now, UpdatedAt: now}
		_, err = conn.Exec(`INSERT INTO engagements (id, target, scope, notes, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			out.ID, out.Target, out.Scope, out.Notes, out.CreatedAt, out.UpdatedAt)
		return err
	})
	return out, err
}

// Engagements lists every engagement, newest first.
func (d *DB) Engagements() ([]Engagement, error) {
	out := []Engagement{}
	err := d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements ORDER BY created_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Engagement
			if err := rows.Scan(&e.ID, &e.Target, &e.Scope, &e.Notes, &e.CreatedAt, &e.UpdatedAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// Target is the engagement a tool call names, by id or by the target's
// own name (the skills pass the unit's name), made if it isn't there yet.
// An empty name is the newest engagement: a session that began one and
// then called a tool without repeating it.
func (d *DB) Target(idOrTarget string) (Engagement, error) {
	name := strings.TrimSpace(idOrTarget)
	if name == "" {
		var newest Engagement
		err := d.do(func(conn *sql.DB) error {
			row := conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements ORDER BY created_at DESC LIMIT 1`)
			err := row.Scan(&newest.ID, &newest.Target, &newest.Scope, &newest.Notes, &newest.CreatedAt, &newest.UpdatedAt)
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("no engagement yet: call redteam_engagement_begin with the target first")
			}
			return err
		})
		return newest, err
	}
	var found Engagement
	err := d.do(func(conn *sql.DB) error {
		row := conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements WHERE id = ?`, name)
		err := row.Scan(&found.ID, &found.Target, &found.Scope, &found.Notes, &found.CreatedAt, &found.UpdatedAt)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row = conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements WHERE target = ?`, name)
		err = row.Scan(&found.ID, &found.Target, &found.Scope, &found.Notes, &found.CreatedAt, &found.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return Engagement{}, err
	}
	if found.ID != "" {
		return found, nil
	}
	// a name no engagement has is a new one: the commander names the target
	// on its first tool call as often as it begins the engagement first
	return d.Begin(name, "", "")
}

// Open reads the engagement an id or a target names, and makes nothing:
// redteam_engagement_begin is the one that creates. An unknown name is an
// error that says so.
func (d *DB) Open(idOrTarget string) (Engagement, error) {
	name := strings.TrimSpace(idOrTarget)
	if name == "" {
		return d.Target("")
	}
	var found Engagement
	err := d.do(func(conn *sql.DB) error {
		row := conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements WHERE id = ?`, name)
		err := row.Scan(&found.ID, &found.Target, &found.Scope, &found.Notes, &found.CreatedAt, &found.UpdatedAt)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row = conn.QueryRow(`SELECT id, target, scope, notes, created_at, updated_at FROM engagements WHERE target = ?`, name)
		err = row.Scan(&found.ID, &found.Target, &found.Scope, &found.Notes, &found.CreatedAt, &found.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("no engagement names " + name + "; begin one with redteam_engagement_begin")
		}
		return err
	})
	if err != nil {
		return Engagement{}, err
	}
	return found, nil
}

// ---------------------------------------------------------------------
// assets
// ---------------------------------------------------------------------

// Asset is one host (and port, when it has one) of an engagement.
type Asset struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	IP         string `json:"ip"`
	Port       int    `json:"port,omitempty"`
	Service    string `json:"service,omitempty"`
	Title      string `json:"title,omitempty"`
	Names      string `json:"names,omitempty"`
	URL        string `json:"url,omitempty"`
	Product    string `json:"product,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Tags       string `json:"tags,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Potential  string `json:"potential,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Status     string `json:"status,omitempty"`
	Blocked    bool   `json:"blocked,omitempty"`
	Test       string `json:"test,omitempty"`
	Surface    string `json:"surface,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// AssetIn is what a tool passes to add or amend an asset.
type AssetIn struct {
	Target     string // host:port, a URL or a host
	Names      string
	Service    string
	Title      string
	Product    string
	Provenance string
	Tool       string
	Scope      string
	Tags       string
	Priority   string
	Potential  string
	Reason     string
	Status     string
	Blocked    *bool
	Test       string
	Surface    string
}

// AddAsset records an asset, or amends the row that host and port already
// have. Amending keeps everything found before: fields the caller leaves
// empty stay as they were, and test and surface are appended to.
func (d *DB) AddAsset(engagement string, in AssetIn) (Asset, error) {
	host, port := hostPort(in.Target)
	if host == "" {
		return Asset{}, errors.New("an asset needs a target (host, host:port or URL)")
	}
	scope := in.Scope
	if scope == "" {
		scope = "internet"
	}
	eng, err := d.Target(engagement)
	if err != nil {
		return Asset{}, err
	}
	var out Asset
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		row := conn.QueryRow(`SELECT id FROM assets WHERE engagement = ? AND ip = ? AND port = ? AND scope = ?`,
			eng.ID, host, port, scope)
		var id string
		switch err := row.Scan(&id); {
		case err == nil:
			// amend: append what is new, keep what the caller didn't send
			sets, args := []string{"updated_at = ?"}, []any{now}
			for _, f := range []struct {
				col  string
				val  string
				grow bool
			}{
				{"service", in.Service, false},
				{"title", in.Title, false},
				{"product", in.Product, false},
				{"tags", in.Tags, true},
				{"priority", in.Priority, false},
				{"potential", in.Potential, false},
				{"reason", in.Reason, false},
				{"status", in.Status, false},
				{"test", in.Test, true},
				{"surface", in.Surface, true},
			} {
				if f.val == "" {
					continue
				}
				if f.grow {
					sets = append(sets, f.col+" = "+joinField(f.col))
					args = append(args, f.val)
					continue
				}
				sets = append(sets, f.col+" = ?")
				args = append(args, f.val)
			}
			if in.Names != "" {
				sets = append(sets, "names = "+joinField("names"))
				args = append(args, in.Names)
			}
			if in.URL() != "" {
				sets = append(sets, "url = ?")
				args = append(args, in.URL())
			}
			if in.Blocked != nil {
				sets = append(sets, "blocked = ?")
				args = append(args, boolInt(*in.Blocked))
			}
			args = append(args, id)
			if _, err := conn.Exec(`UPDATE assets SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
				return err
			}
			return nil
		case errors.Is(err, sql.ErrNoRows):
			id = newID("ast")
			_, err := conn.Exec(`INSERT INTO assets
				(id, engagement, ip, port, service, title, names, url, product, provenance, tool, scope, tags, priority, potential, reason, status, blocked, test, surface, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, eng.ID, host, port, in.Service, in.Title, in.Names, in.URL(), in.Product,
				def(in.Provenance, "passive"), in.Tool, scope, in.Tags, in.Priority, in.Potential,
				in.Reason, def(in.Status, "new"), boolIntPtr(in.Blocked), in.Test, in.Surface, now, now)
			if err != nil {
				return err
			}
			// 按 /24 自动建 C 段 and the edge from it, as the skills promise
			return d.linkSegment(conn, eng.ID, id, host, now)
		default:
			return err
		}
	})
	if err != nil {
		return Asset{}, err
	}
	out, err = d.asset(eng.ID, "", in, host, port, scope)
	return out, err
}

// URL is the asset's own URL, when the target was one.
func (in AssetIn) URL() string {
	t := strings.TrimSpace(in.Target)
	if strings.Contains(t, "://") {
		return t
	}
	return ""
}

// asset reads back the row AddAsset wrote.
func (d *DB) asset(engagement, id string, in AssetIn, host string, port int, scope string) (Asset, error) {
	var a Asset
	err := d.do(func(conn *sql.DB) error {
		q := `SELECT id, engagement, ip, port, service, title, names, url, product, provenance, tool, scope, tags, priority, potential, reason, status, blocked, test, surface, created_at, updated_at
			FROM assets WHERE engagement = ?`
		args := []any{engagement}
		if id != "" {
			q += ` AND id = ?`
			args = append(args, id)
		} else {
			q += ` AND ip = ? AND port = ? AND scope = ?`
			args = append(args, host, port, scope)
		}
		row := conn.QueryRow(q, args...)
		var blocked int
		if err := row.Scan(&a.ID, &a.Engagement, &a.IP, &a.Port, &a.Service, &a.Title, &a.Names,
			&a.URL, &a.Product, &a.Provenance, &a.Tool, &a.Scope, &a.Tags, &a.Priority, &a.Potential,
			&a.Reason, &a.Status, &blocked, &a.Test, &a.Surface, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return err
		}
		a.Blocked = blocked != 0
		return nil
	})
	return a, err
}

// linkSegment adds the /24 an address is in as an asset of its own and an
// edge from it to the address ("contains"), once. Only IPv4 gets one: a
// /24 of a v6 address isn't a network anybody scans.
func (d *DB) linkSegment(conn *sql.DB, engagement, assetID, host string, now int64) error {
	seg := segment(host)
	if seg == "" {
		return nil
	}
	segID := newID("ast")
	// the segment's row is an address's /24, not something an agent found,
	// so it takes no side of the boundary: it is "segment" on both, and a
	// host found from either side links to the one row
	if _, err := conn.Exec(`INSERT INTO assets
		(id, engagement, ip, port, service, title, provenance, scope, tags, status, created_at, updated_at)
		VALUES (?,?,?,0,'','','active','segment','segment','new',?,?)
		ON CONFLICT(engagement, ip, port, scope) DO NOTHING`,
		segID, engagement, seg, now, now); err != nil {
		return err
	}
	var existing string
	if err := conn.QueryRow(`SELECT id FROM assets WHERE engagement = ? AND ip = ? AND port = 0 AND scope = 'segment'`,
		engagement, seg).Scan(&existing); err != nil {
		return err
	}
	_, err := conn.Exec(`INSERT INTO asset_edges (id, engagement, from_asset, to_asset, rel, created_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT DO NOTHING`, newID("edg"), engagement, existing, assetID, "contains", now)
	return err
}

// Assets lists an engagement's assets. A segment comes after the addresses
// in it (a port of 0 and the tag "segment" mark one), so the report reads
// outside in.
func (d *DB) Assets(engagement string) ([]Asset, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Asset
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, ip, port, service, title, names, url, product, provenance, tool, scope, tags, priority, potential, reason, status, blocked, test, surface, created_at, updated_at
			FROM assets WHERE engagement = ?
			ORDER BY CASE WHEN tags = 'segment' THEN 1 ELSE 0 END, priority DESC, ip, port`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Asset
			var blocked int
			if err := rows.Scan(&a.ID, &a.Engagement, &a.IP, &a.Port, &a.Service, &a.Title, &a.Names,
				&a.URL, &a.Product, &a.Provenance, &a.Tool, &a.Scope, &a.Tags, &a.Priority, &a.Potential,
				&a.Reason, &a.Status, &blocked, &a.Test, &a.Surface, &a.CreatedAt, &a.UpdatedAt); err != nil {
				return err
			}
			a.Blocked = blocked != 0
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// AssetQuery finds an engagement's assets by host, host:port, service or
// a word in the title. A query with nothing in it lists them all.
func (d *DB) AssetQuery(engagement, query string) ([]Asset, error) {
	all, err := d.Assets(engagement)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all, nil
	}
	host, port := hostPort(q)
	var out []Asset
	for _, a := range all {
		switch {
		case a.IP == q || (host != "" && a.IP == host && (port == 0 || a.Port == port)):
			out = append(out, a)
		case strings.Contains(strings.ToLower(a.Service+" "+a.Title+" "+a.Product+" "+a.Names+" "+a.Tags), q):
			out = append(out, a)
		}
	}
	return out, nil
}

// TestAsset writes what was tried on an asset and what it turned up,
// appended to what the last attempt said — the skills' "避免后来者重复打".
func (d *DB) TestAsset(engagement, target, status, test, surface string, blocked *bool) (Asset, error) {
	return d.AddAsset(engagement, AssetIn{Target: target, Status: status, Test: test, Surface: surface, Blocked: blocked})
}

// LinkAssets records an edge between two assets of an engagement.
func (d *DB) LinkAssets(engagement, from, to, rel string) error {
	eng, err := d.Target(engagement)
	if err != nil {
		return err
	}
	fromID, err := d.findAsset(eng.ID, from)
	if err != nil {
		return err
	}
	toID, err := d.findAsset(eng.ID, to)
	if err != nil {
		return err
	}
	if rel == "" {
		rel = "related"
	}
	return d.do(func(conn *sql.DB) error {
		_, err := conn.Exec(`INSERT INTO asset_edges (id, engagement, from_asset, to_asset, rel, created_at)
			VALUES (?,?,?,?,?,?) ON CONFLICT DO NOTHING`, newID("edg"), eng.ID, fromID, toID, rel, Now())
		return err
	})
}

// findAsset is the id of the asset a target names, made if it isn't there.
func (d *DB) findAsset(engagement, target string) (string, error) {
	list, err := d.Assets(engagement)
	if err != nil {
		return "", err
	}
	// an asset's own id names it directly
	for _, a := range list {
		if a.ID == target {
			return a.ID, nil
		}
	}
	host, port := hostPort(target)
	for _, a := range list {
		if a.IP == host && a.Port == port {
			return a.ID, nil
		}
	}
	if host == "" {
		return "", fmt.Errorf("no asset %q to link", target)
	}
	a, err := d.AddAsset(engagement, AssetIn{Target: target})
	return a.ID, err
}

// Edge is one relation between two assets.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
}

// Edges lists an engagement's asset relations.
func (d *DB) Edges(engagement string) ([]Edge, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Edge
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT from_asset, to_asset, rel FROM asset_edges WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Edge
			if err := rows.Scan(&e.From, &e.To, &e.Rel); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// joinField is the SQL that appends a value to a multi-line field, with
// the one line break between them: `col = col || ? || value` keeps the
// field when the value is empty.
func joinField(col string) string {
	return "NULLIF(TRIM(COALESCE(" + col + ",'') || CASE WHEN COALESCE(" + col + ",'') = '' THEN '' ELSE char(10) END || ?), '')"
}

func def(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolIntPtr(b *bool) int {
	if b != nil && *b {
		return 1
	}
	return 0
}

// AssetGet reads one asset: by its id, or by what a host or host:port
// query finds. The empty answer is an error that says what to search by,
// not an empty object.
func (d *DB) AssetGet(engagement, id, query string) (Asset, error) {
	var zero Asset
	if id != "" {
		list, err := d.Assets(engagement)
		if err != nil {
			return zero, err
		}
		for _, a := range list {
			if a.ID == id {
				return a, nil
			}
		}
		return zero, errors.New("no asset with id " + id + " in this engagement")
	}
	if query == "" {
		return zero, errors.New("an asset needs an id, or a host / host:port to find it by")
	}
	list, err := d.AssetQuery(engagement, query)
	if err != nil {
		return zero, err
	}
	if len(list) == 0 {
		return zero, errors.New("no asset matches " + query + "; record it with redteam_asset_add first")
	}
	if len(list) > 1 {
		// several ports of one host: the widest row (the host without a
		// port) is what "取资产" means here
		for _, a := range list {
			if a.Port == 0 && a.Scope != "segment" {
				return a, nil
			}
		}
	}
	return list[0], nil
}

// AssetStats counts the engagement's assets: total, per scope, per status
// and per priority, and the segments with hosts in them. The collection
// phase's closing check.
func (d *DB) AssetStats(engagement string) (map[string]any, error) {
	list, err := d.Assets(engagement)
	if err != nil {
		return nil, err
	}
	stats := map[string]any{
		"total": 0, "internet": 0, "internal": 0, "segments": 0,
		"status": map[string]int{}, "priority": map[string]int{},
		"services": map[string]int{}, "owned": 0,
	}
	segments := map[string]int{}
	for _, a := range list {
		if a.Scope == "segment" {
			stats["segments"] = stats["segments"].(int) + 1
			segments[a.IP] = 0
			continue
		}
		stats["total"] = stats["total"].(int) + 1
		switch a.Scope {
		case "internet":
			stats["internet"] = stats["internet"].(int) + 1
		case "internal":
			stats["internal"] = stats["internal"].(int) + 1
		}
		if a.Status == "owned" {
			stats["owned"] = stats["owned"].(int) + 1
		}
		st := stats["status"].(map[string]int)
		if a.Status == "" {
			a.Status = "new"
		}
		st[a.Status]++
		pr := stats["priority"].(map[string]int)
		if a.Priority != "" {
			pr[a.Priority]++
		}
		if a.Service != "" {
			sv := stats["services"].(map[string]int)
			sv[a.Service]++
		}
	}
	// count the hosts in each segment
	for _, a := range list {
		if a.Scope == "segment" {
			continue
		}
		if seg := segment(a.IP); seg != "" {
			if _, ok := segments[seg]; ok {
				segments[seg]++
			}
		}
	}
	type segRow struct {
		CIDR  string `json:"cidr"`
		Hosts int    `json:"hosts"`
	}
	segList := []segRow{}
	for cidr, n := range segments {
		segList = append(segList, segRow{cidr, n})
	}
	sort.Slice(segList, func(i, j int) bool { return segList[i].CIDR < segList[j].CIDR })
	stats["segmentList"] = segList
	return stats, nil
}

// AssetTimeline groups the engagement's assets by the day they were found
// (internal and internet apart) and lists the latest ones.
func (d *DB) AssetTimeline(engagement string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	list, err := d.Assets(engagement)
	if err != nil {
		return nil, err
	}
	type dayRow struct {
		Day      string `json:"day"`
		Internet int    `json:"internet"`
		Internal int    `json:"internal"`
	}
	byDay := map[string]*dayRow{}
	type recent struct {
		ID      string `json:"id"`
		Target  string `json:"target"`
		Scope   string `json:"scope,omitempty"`
		FoundAt int64  `json:"foundAt"`
	}
	latest := []recent{}
	for _, a := range list {
		if a.Scope == "segment" {
			continue
		}
		day := time.Unix(a.CreatedAt/1000, 0).UTC().Format("2006-01-02")
		row := byDay[day]
		if row == nil {
			row = &dayRow{Day: day}
			byDay[day] = row
		}
		if a.Scope == "internal" {
			row.Internal++
		} else {
			row.Internet++
		}
		latest = append(latest, recent{a.ID, a.IP + hostSuffix(a.Port), a.Scope, a.CreatedAt})
	}
	days := []dayRow{}
	for _, row := range byDay {
		days = append(days, *row)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
	sort.Slice(latest, func(i, j int) bool { return latest[i].FoundAt > latest[j].FoundAt })
	if len(latest) > limit {
		latest = latest[:limit]
	}
	return map[string]any{"days": days, "latest": latest}, nil
}

// AssetGraph is the engagement as nodes and edges: every segment, every
// asset in it, and every edge between them. A graph is what the lateral
// movement plan reads.
func (d *DB) AssetGraph(engagement string) (map[string]any, error) {
	list, err := d.Assets(engagement)
	if err != nil {
		return nil, err
	}
	type node struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Target string `json:"target,omitempty"`
		Scope  string `json:"scope,omitempty"`
		Status string `json:"status,omitempty"`
	}
	nodes := []node{}
	byID := map[string]bool{}
	for _, a := range list {
		kind := "host"
		if a.Scope == "segment" {
			kind = "segment"
		}
		nodes = append(nodes, node{a.ID, kind, a.IP + hostSuffix(a.Port), a.Scope, a.Status})
		byID[a.ID] = true
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Rel  string `json:"rel"`
	}
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	edges := []edge{}
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT from_asset, to_asset, rel FROM asset_edges WHERE engagement = ?`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e edge
			if err := rows.Scan(&e.From, &e.To, &e.Rel); err != nil {
				return err
			}
			edges = append(edges, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	// an edge naming an asset outside the list (an amend dropped it) is
	// left out: a graph that names absent nodes isn't drawable
	kept := edges[:0]
	for _, e := range edges {
		if byID[e.From] && byID[e.To] {
			kept = append(kept, e)
		}
	}
	return map[string]any{"nodes": nodes, "edges": kept}, nil
}

// hostSuffix is a port as ":443", or "" without one — the read side of a
// target string.
func hostSuffix(port int) string {
	if port <= 0 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}
