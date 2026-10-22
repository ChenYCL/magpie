// Package redteamstore is where a red-team engagement is written down as
// it runs: the target's assets, what each of them is and what was found on
// it, the credentials, tunnels, shells and sessions gained, every action
// taken with the command that took it, and the points scored — in one
// SQLite database, so a later turn, a later session or another agent reads
// what an earlier one did instead of being told it in the conversation.
//
// The skills under deploy/magpie/config/redteam-skills call it as
// redteam_* tools over the MCP server at gateway.RedteamMCPPath
// (tools.go holds their schemas); the presets' commander reads it back to
// check what a subagent reported, and the window's page lists it
// read-only.
//
// Two rules hold everywhere here:
//
//   - A record is written once and amended, never deleted. A rescan adds to
//     an asset, it does not replace what was found before (#the skill's
//     "已测过什么，避免重复打").
//   - A store that can't be read is an error, never an empty store: a page
//     or a report that shows nothing because the database failed would read
//     as "nothing was found", which is the one thing a report must not say
//     wrongly.
package redteamstore

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	_ "github.com/yetone/magpie/internal/sqlitedb" // the one way magpie opens SQLite
)

// File is the database, by name in magpie's config folder.
const File = "redteam.db"

// Path is the database's path.
func Path() string { return filepath.Join(appdir.Config(), File) }

// Now is the clock the store stamps rows with. A test replaces it.
var Now = func() int64 { return time.Now().UnixMilli() }

// ErrNoRecord is what an amend of a row that isn't there answers.
var ErrNoRecord = errors.New("no such record")

// DB is the store: one connection to redteam.db, opened on first use and
// migrated then.
type DB struct {
	mu   sync.Mutex
	path string
	conn *sql.DB
}

// Open opens the store at path, migrating it. An empty path is Path().
// The connection is opened at once, so a store that can't be opened is an
// error here rather than at the first write.
func Open(path string) (*DB, error) {
	if path == "" {
		path = Path()
	}
	d := &DB{path: path}
	if err := d.connect(); err != nil {
		return nil, err
	}
	return d, nil
}

// Close lets go of the connection.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close()
	d.conn = nil
	return err
}

// connect opens the database and makes sure its schema is current.
func (d *DB) connect() error {
	if d.conn != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(d.path),
		RawQuery: "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"}
	if len(u.Path) >= 2 && u.Path[1] == ':' {
		u.Path = "/" + u.Path // Windows drive letter
	}
	conn, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	// one writer at a time is what SQLite wants; the tools are called from
	// a single MCP request at a time in practice, and serializing keeps a
	// busier moment from answering SQLITE_BUSY
	conn.SetMaxOpenConns(1)
	if err := migrate(conn); err != nil {
		conn.Close()
		return err
	}
	d.conn = conn
	return nil
}

// do runs fn with the connection, opening it if need be.
func (d *DB) do(fn func(*sql.DB) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.connect(); err != nil {
		return err
	}
	return fn(d.conn)
}

// schema is the whole store. Every table keeps what was found: a row is
// amended by an update, never removed by a rescan.
const schema = `
CREATE TABLE IF NOT EXISTS engagements (
	id         TEXT PRIMARY KEY,
	target     TEXT NOT NULL,
	scope      TEXT NOT NULL DEFAULT '',   -- what the operator authorized, verbatim
	notes      TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS assets (
	id          TEXT PRIMARY KEY,
	engagement  TEXT NOT NULL,
	ip          TEXT NOT NULL DEFAULT '',
	port        INTEGER NOT NULL DEFAULT 0,
	service     TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL DEFAULT '',
	names       TEXT NOT NULL DEFAULT '',   -- comma-separated domains, as found
	url         TEXT NOT NULL DEFAULT '',
	product     TEXT NOT NULL DEFAULT '',
	provenance  TEXT NOT NULL DEFAULT 'passive', -- passive | active
	tool        TEXT NOT NULL DEFAULT '',
	scope       TEXT NOT NULL DEFAULT 'internet', -- internet | internal
	tags        TEXT NOT NULL DEFAULT '',
	priority    TEXT NOT NULL DEFAULT '',
	potential   TEXT NOT NULL DEFAULT '',
	reason      TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'new', -- new | testing | owned | abandoned
	blocked     INTEGER NOT NULL DEFAULT 0,
	test        TEXT NOT NULL DEFAULT '',  -- what was tried, one line per attempt
	surface     TEXT NOT NULL DEFAULT '',  -- what an attempt found to attack next
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS assets_key ON assets(engagement, ip, port, scope);
CREATE INDEX IF NOT EXISTS assets_engagement ON assets(engagement);

CREATE TABLE IF NOT EXISTS asset_edges (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	from_asset TEXT NOT NULL,
	to_asset   TEXT NOT NULL,
	rel        TEXT NOT NULL,             -- resolves | contains | same-cert | …
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS asset_edges_engagement ON asset_edges(engagement);

CREATE TABLE IF NOT EXISTS vulns (
	id          TEXT PRIMARY KEY,
	engagement  TEXT NOT NULL,
	target      TEXT NOT NULL,            -- host:port
	name        TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL DEFAULT '', -- n-day | 1-day | unauth | weak-cred | config
	severity    TEXT NOT NULL DEFAULT '',
	confidence  TEXT NOT NULL DEFAULT '', -- confirmed | likely | suspected
	gained      TEXT NOT NULL DEFAULT '',
	evidence    TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'open', -- open | exploited | fixed | false-positive
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS vulns_engagement ON vulns(engagement);
CREATE INDEX IF NOT EXISTS vulns_target ON vulns(engagement, target);

CREATE TABLE IF NOT EXISTS credentials (
	id           TEXT PRIMARY KEY,
	engagement   TEXT NOT NULL,
	target       TEXT NOT NULL DEFAULT '', -- host:port it works on
	user         TEXT NOT NULL DEFAULT '',
	secret_kind  TEXT NOT NULL DEFAULT 'password', -- password | hash | key | token | cookie
	secret_value TEXT NOT NULL DEFAULT '',
	source       TEXT NOT NULL DEFAULT '', -- where it came from (dump file, brute)
	secret_ref   TEXT NOT NULL DEFAULT '', -- the runs/ file with the evidence
	privilege    TEXT NOT NULL DEFAULT '',
	verified     INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS credentials_engagement ON credentials(engagement);

CREATE TABLE IF NOT EXISTS services_entries (
	id          TEXT PRIMARY KEY,
	engagement  TEXT NOT NULL,
	target      TEXT NOT NULL DEFAULT '',
	user        TEXT NOT NULL DEFAULT '',
	password    TEXT NOT NULL DEFAULT '',
	method      TEXT NOT NULL DEFAULT '', -- web-login | reverse-shell | ssh | sql | pth …
	listen      TEXT NOT NULL DEFAULT '', -- the listener a shell came back to
	privilege   TEXT NOT NULL DEFAULT '',
	session     TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS services_entries_engagement ON services_entries(engagement);

CREATE TABLE IF NOT EXISTS tunnels (
	id          TEXT PRIMARY KEY,
	engagement  TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT '', -- socks5 | port-forward
	entry_kind  TEXT NOT NULL DEFAULT '', -- target-outbound | target-http | target-agent
	listen      TEXT NOT NULL DEFAULT '', -- the local end, e.g. 127.0.0.1:1080
	via         TEXT NOT NULL DEFAULT '', -- the asset or webshell it runs through
	reachable   TEXT NOT NULL DEFAULT '', -- the networks it was measured to reach
	status      TEXT NOT NULL DEFAULT 'down', -- up | down
	last_check  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tunnels_engagement ON tunnels(engagement);

CREATE TABLE IF NOT EXISTS webshells (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	url        TEXT NOT NULL DEFAULT '',
	shell_type TEXT NOT NULL DEFAULT '', -- behinder | godzilla | antsword | custom
	pass_key   TEXT NOT NULL DEFAULT '',
	privilege  TEXT NOT NULL DEFAULT '',
	verified   INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS webshells_engagement ON webshells(engagement);

CREATE TABLE IF NOT EXISTS pocs (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	category      TEXT NOT NULL DEFAULT '',
	engagement    TEXT NOT NULL DEFAULT '', -- which engagement first verified it
	asset_target  TEXT NOT NULL DEFAULT '',
	severity      TEXT NOT NULL DEFAULT '',
	content       TEXT NOT NULL DEFAULT '', -- the script or request, redacted
	verified_note TEXT NOT NULL DEFAULT '',
	verified      INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS pocs_name ON pocs(name);

CREATE TABLE IF NOT EXISTS scores (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	category   TEXT NOT NULL,            -- server-host | web-app | db-credential | central-system
	target     TEXT NOT NULL,
	points     INTEGER NOT NULL DEFAULT 0,
	asset_id   TEXT NOT NULL DEFAULT '',
	evidence   TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
-- one score per (category, target): the same host taken twice scores once
CREATE UNIQUE INDEX IF NOT EXISTS scores_once ON scores(engagement, category, target);

CREATE TABLE IF NOT EXISTS chains (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	stage_code TEXT NOT NULL DEFAULT '',  -- recon | internet | boundary | internal
	role       TEXT NOT NULL DEFAULT '',
	tool       TEXT NOT NULL DEFAULT '',  -- the command, verbatim
	result     TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS chains_engagement ON chains(engagement);

CREATE TABLE IF NOT EXISTS evidence (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	kind       TEXT NOT NULL DEFAULT 'http',
	request    TEXT NOT NULL DEFAULT '',
	response   TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS evidence_engagement ON evidence(engagement);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	how        TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'live', -- live | lost
	last_check INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS sessions_name ON sessions(engagement, name);

CREATE TABLE IF NOT EXISTS attack_files (
	id         TEXT PRIMARY KEY,
	engagement TEXT NOT NULL,
	name       TEXT NOT NULL,
	path       TEXT NOT NULL DEFAULT '', -- where it is on this machine
	purpose    TEXT NOT NULL DEFAULT '',
	delivered  TEXT NOT NULL DEFAULT '', -- the asset it was put on
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attack_files_engagement ON attack_files(engagement);
`

// migrate makes every table and index there.
func migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("redteam store schema: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------
// small helpers the tools share
// ---------------------------------------------------------------------

// hostPort splits "host:port", "host", "[v6]:port" or a URL into the host
// and the port (0 when there is none). It never guesses a port.
func hostPort(s string) (string, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			port := 0
			if p := u.Port(); p != "" {
				port = atoi(p)
			}
			return u.Hostname(), port
		}
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, atoi(p)
	}
	return strings.Trim(s, "[]"), 0
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// segment is the /24 (or /16 for a v6 /64 cut) a host belongs to, as the
// skills' "按 /24 自动建 C 段" needs: the network an address is in, or ""
// when it isn't an address.
func segment(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.0/24", v4[0], v4[1], v4[2])
	}
	return ""
}

// newID makes a row's id: the time and a counter, so a list is in the
// order things happened without reading created_at twice.
func newID(prefix string) string {
	idMu.Lock()
	idSeq++
	n := idSeq
	idMu.Unlock()
	return fmt.Sprintf("%s-%013d-%04d", prefix, Now(), n%10000)
}

var (
	idMu  sync.Mutex
	idSeq int64
)

// splitList splits a comma or newline separated field into its parts.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
