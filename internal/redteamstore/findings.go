package redteamstore

import (
	"database/sql"
	"errors"
	"strings"
)

// Vuln is one finding on one target of an engagement.
type Vuln struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Target     string `json:"target"`
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Gained     string `json:"gained,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Status     string `json:"status,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// VulnIn is what a tool passes to add or amend a vuln. An ID amends that
// row; without one the target and name find it.
type VulnIn struct {
	ID         string
	Target     string
	Name       string
	Kind       string
	Severity   string
	Confidence string
	Gained     string
	Evidence   string
	Status     string
}

// AddVuln records a vuln, or amends the row for that target and name.
// Fields left empty keep what the row has: a finding is widened, never
// replaced.
func (d *DB) AddVuln(engagement string, in VulnIn) (Vuln, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Vuln{}, err
	}
	in.Target = strings.TrimSpace(in.Target)
	// a finding is widened, not moved: an amend names the row and what
	// changed, and keeps the target it was found on
	if in.ID == "" {
		if in.Target == "" {
			return Vuln{}, errors.New("a vuln needs a target (host:port)")
		}
		if strings.TrimSpace(in.Name) == "" {
			return Vuln{}, errors.New("a vuln needs a name")
		}
	}
	var id string
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		id = in.ID
		if id == "" {
			row := conn.QueryRow(`SELECT id FROM vulns WHERE engagement = ? AND target = ? AND name = ? ORDER BY created_at LIMIT 1`,
				eng.ID, in.Target, in.Name)
			var found string
			switch err := row.Scan(&found); {
			case err == nil:
				id = found
			case errors.Is(err, sql.ErrNoRows):
				id = newID("vul")
			default:
				return err
			}
		}
		sets, args := updates(map[string]string{
			"name": in.Name, "kind": in.Kind, "severity": in.Severity, "confidence": in.Confidence,
			"gained": in.Gained, "evidence": in.Evidence, "status": in.Status,
		}, now)
		if in.ID != "" {
			args = append(args, in.ID, eng.ID)
			res, err := conn.Exec(`UPDATE vulns SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNoRecord
			}
			return nil
		}
		// updating by target and name: nothing to update makes the row
		args = append(args, id, eng.ID)
		res, err := conn.Exec(`UPDATE vulns SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		_, err = conn.Exec(`INSERT INTO vulns
			(id, engagement, target, name, kind, severity, confidence, gained, evidence, status, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, eng.ID, in.Target, in.Name, in.Kind, in.Severity, in.Confidence, in.Gained,
			in.Evidence, def(in.Status, "open"), now, now)
		return err
	})
	if err != nil {
		return Vuln{}, err
	}
	return d.Vuln(eng.ID, id)
}

// Vuln reads one row back.
func (d *DB) Vuln(engagement, id string) (Vuln, error) {
	var v Vuln
	err := d.do(func(conn *sql.DB) error {
		return conn.QueryRow(`SELECT id, engagement, target, name, kind, severity, confidence, gained, evidence, status, created_at, updated_at
			FROM vulns WHERE id = ? AND engagement = ?`, id, engagement).
			Scan(&v.ID, &v.Engagement, &v.Target, &v.Name, &v.Kind, &v.Severity, &v.Confidence,
				&v.Gained, &v.Evidence, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	})
	return v, err
}

// Vulns lists an engagement's findings, worst first.
func (d *DB) Vulns(engagement string) ([]Vuln, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Vuln
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, name, kind, severity, confidence, gained, evidence, status, created_at, updated_at
			FROM vulns WHERE engagement = ?
			ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
				created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v Vuln
			if err := rows.Scan(&v.ID, &v.Engagement, &v.Target, &v.Name, &v.Kind, &v.Severity,
				&v.Confidence, &v.Gained, &v.Evidence, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

// ---------------------------------------------------------------------
// credentials
// ---------------------------------------------------------------------

// Credential is a password, hash, key or token that works somewhere. Its
// value is kept as given: an agent reuses it on the next host, and a
// masked one is useless.
type Credential struct {
	ID          string `json:"id"`
	Engagement  string `json:"engagement"`
	Target      string `json:"target,omitempty"`
	User        string `json:"user,omitempty"`
	SecretKind  string `json:"secretKind,omitempty"`
	SecretValue string `json:"secretValue,omitempty"`
	Source      string `json:"source,omitempty"`
	SecretRef   string `json:"secretRef,omitempty"`
	Privilege   string `json:"privilege,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// CredentialIn is what a tool passes to add or amend a credential.
type CredentialIn struct {
	ID          string
	Target      string
	User        string
	SecretKind  string
	SecretValue string
	Source      string
	SecretRef   string
	Privilege   string
	Verified    *bool
}

// AddCredential keeps a credential, or amends one by id. An amend keeps
// every field it isn't sent.
func (d *DB) AddCredential(engagement string, in CredentialIn) (Credential, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Credential{}, err
	}
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		if in.ID != "" {
			sets, args := updates(map[string]string{
				"target": in.Target, "user": in.User, "secret_kind": in.SecretKind,
				"secret_value": in.SecretValue, "source": in.Source, "secret_ref": in.SecretRef,
				"privilege": in.Privilege,
			}, now)
			if in.Verified != nil {
				sets = append(sets, "verified = ?")
				args = append(args, boolInt(*in.Verified))
			}
			args = append(args, in.ID, eng.ID)
			res, err := conn.Exec(`UPDATE credentials SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNoRecord
			}
			return nil
		}
		if strings.TrimSpace(in.SecretValue) == "" {
			return errors.New("a credential needs a secret_value")
		}
		// the same secret on the same host is one row: a dump read twice
		// doesn't make two credentials
		row := conn.QueryRow(`SELECT id FROM credentials WHERE engagement = ? AND target = ? AND user = ? AND secret_value = ? LIMIT 1`,
			eng.ID, in.Target, in.User, in.SecretValue)
		var existing string
		switch err := row.Scan(&existing); {
		case err == nil:
			in.ID = existing
			return d.amendCredential(conn, eng.ID, in, now)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err := conn.Exec(`INSERT INTO credentials
			(id, engagement, target, user, secret_kind, secret_value, source, secret_ref, privilege, verified, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID("crd"), eng.ID, in.Target, in.User, def(in.SecretKind, "password"), in.SecretValue,
			in.Source, in.SecretRef, in.Privilege, boolIntPtr(in.Verified), now, now)
		return err
	})
	if err != nil {
		return Credential{}, err
	}
	return d.credential(eng.ID, in)
}

// amendCredential updates an existing row from in.
func (d *DB) amendCredential(conn *sql.DB, engagement string, in CredentialIn, now int64) error {
	sets, args := updates(map[string]string{
		"target": in.Target, "user": in.User, "secret_kind": in.SecretKind,
		"secret_value": in.SecretValue, "source": in.Source, "secret_ref": in.SecretRef,
		"privilege": in.Privilege,
	}, now)
	if in.Verified != nil {
		sets = append(sets, "verified = ?")
		args = append(args, boolInt(*in.Verified))
	}
	args = append(args, in.ID, engagement)
	_, err := conn.Exec(`UPDATE credentials SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
	return err
}

// credential reads back the row AddCredential wrote: by id when it was
// given, else by the secret it kept.
func (d *DB) credential(engagement string, in CredentialIn) (Credential, error) {
	var c Credential
	err := d.do(func(conn *sql.DB) error {
		q := `SELECT id, engagement, target, user, secret_kind, secret_value, source, secret_ref, privilege, verified, created_at, updated_at
			FROM credentials WHERE engagement = ?`
		args := []any{engagement}
		if in.ID != "" {
			q += ` AND id = ?`
			args = append(args, in.ID)
		} else {
			q += ` AND target = ? AND user = ? AND secret_value = ?`
			args = append(args, in.Target, in.User, in.SecretValue)
		}
		var verified int
		if err := conn.QueryRow(q, args...).Scan(&c.ID, &c.Engagement, &c.Target, &c.User, &c.SecretKind,
			&c.SecretValue, &c.Source, &c.SecretRef, &c.Privilege, &verified, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		c.Verified = verified != 0
		return nil
	})
	return c, err
}

// Credentials lists an engagement's credentials. The secret is left out
// of the list a report shows unless withSecrets says otherwise: a list
// that goes into a report isn't where a plaintext belongs.
func (d *DB) Credentials(engagement string, withSecrets bool) ([]Credential, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Credential
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, user, secret_kind, secret_value, source, secret_ref, privilege, verified, created_at, updated_at
			FROM credentials WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Credential
			var verified int
			if err := rows.Scan(&c.ID, &c.Engagement, &c.Target, &c.User, &c.SecretKind, &c.SecretValue,
				&c.Source, &c.SecretRef, &c.Privilege, &verified, &c.CreatedAt, &c.UpdatedAt); err != nil {
				return err
			}
			c.Verified = verified != 0
			if !withSecrets {
				c.SecretValue = maskSecret(c.SecretValue)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// maskSecret keeps the first and last character of a secret, so a report
// can say which one it was without carrying it.
func maskSecret(s string) string {
	r := []rune(s)
	switch {
	case len(r) == 0:
		return ""
	case len(r) <= 4:
		return strings.Repeat("*", len(r))
	}
	return string(r[0]) + strings.Repeat("*", len(r)-2) + string(r[len(r)-1])
}

// updates are the SET parts for the fields in values that aren't empty,
// with updated_at always set.
func updates(values map[string]string, now int64) ([]string, []any) {
	sets := []string{"updated_at = ?"}
	args := []any{now}
	// a fixed order, so the same call writes the same SQL: a map's order
	// isn't the same twice
	for _, col := range []string{"name", "kind", "severity", "confidence", "gained", "evidence",
		"status", "target", "user", "secret_kind", "secret_value", "source", "secret_ref",
		"privilege", "content", "verified_note", "category", "reachable", "listen", "via",
		"entry_kind", "url", "shell_type", "pass_key", "method", "session", "purpose",
		"delivered", "path", "notes", "scope"} {
		v, ok := values[col]
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	return sets, args
}

// fmtInt is an integer as SQL takes it.
func fmtInt(n int) any { return n }

// ---------------------------------------------------------------------
// entries, tunnels, webshells, sessions
// ---------------------------------------------------------------------

// Entry is an access gained: how it was got, as whom, and on what.
type Entry struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Target     string `json:"target,omitempty"`
	User       string `json:"user,omitempty"`
	Password   string `json:"password,omitempty"`
	Method     string `json:"method,omitempty"`
	Listen     string `json:"listen,omitempty"`
	Privilege  string `json:"privilege,omitempty"`
	Session    string `json:"session,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// EntryIn is what a tool passes to record an access.
type EntryIn struct {
	Target    string
	User      string
	Password  string
	Method    string
	Listen    string
	Privilege string
	Session   string
}

// AddEntry records an access gained.
func (d *DB) AddEntry(engagement string, in EntryIn) (Entry, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Entry{}, err
	}
	if in.Target == "" && in.Listen == "" {
		return Entry{}, errors.New("an access needs a target or a listen address")
	}
	id := newID("acc")
	var out Entry
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		_, err := conn.Exec(`INSERT INTO services_entries
			(id, engagement, target, user, password, method, listen, privilege, session, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, eng.ID, in.Target, in.User, in.Password, in.Method, in.Listen, in.Privilege, in.Session, now, now)
		return err
	})
	if err != nil {
		return Entry{}, err
	}
	out, err = d.Entry(eng.ID, id)
	return out, err
}

// Entry reads one back.
func (d *DB) Entry(engagement, id string) (Entry, error) {
	var e Entry
	err := d.do(func(conn *sql.DB) error {
		return conn.QueryRow(`SELECT id, engagement, target, user, password, method, listen, privilege, session, created_at, updated_at
			FROM services_entries WHERE id = ? AND engagement = ?`, id, engagement).
			Scan(&e.ID, &e.Engagement, &e.Target, &e.User, &e.Password, &e.Method, &e.Listen,
				&e.Privilege, &e.Session, &e.CreatedAt, &e.UpdatedAt)
	})
	return e, err
}

// Entries lists an engagement's accesses.
func (d *DB) Entries(engagement string) ([]Entry, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Entry
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, user, password, method, listen, privilege, session, created_at, updated_at
			FROM services_entries WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.ID, &e.Engagement, &e.Target, &e.User, &e.Password, &e.Method,
				&e.Listen, &e.Privilege, &e.Session, &e.CreatedAt, &e.UpdatedAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// Tunnel is an entry into a network, and the networks it was measured to
// reach.
type Tunnel struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Kind       string `json:"kind,omitempty"`
	EntryKind  string `json:"entryKind,omitempty"`
	Listen     string `json:"listen,omitempty"`
	Via        string `json:"via,omitempty"`
	Reachable  string `json:"reachable,omitempty"`
	Status     string `json:"status,omitempty"`
	LastCheck  int64  `json:"lastCheck,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// TunnelIn is what a tool passes to record or amend a tunnel.
type TunnelIn struct {
	ID        string
	Kind      string
	EntryKind string
	Listen    string
	Via       string
	Reachable string
	Status    string
}

// AddTunnel records a tunnel, or amends one by id. A check of a tunnel's
// reachability is an amend with status and reachable.
func (d *DB) AddTunnel(engagement string, in TunnelIn) (Tunnel, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Tunnel{}, err
	}
	id := in.ID
	var out Tunnel
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		if id != "" {
			sets, args := updates(map[string]string{
				"listen": in.Listen, "via": in.Via, "reachable": in.Reachable,
				"entry_kind": in.EntryKind, "kind": in.Kind, "status": in.Status,
			}, now)
			sets = append(sets, "last_check = ?")
			args = append(args, now, id, eng.ID)
			res, err := conn.Exec(`UPDATE tunnels SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNoRecord
			}
			return nil
		}
		if in.Listen == "" {
			return errors.New("a tunnel needs its listen address")
		}
		// the same listen address is one tunnel: a re-registration after a
		// restart amends it rather than making a second
		row := conn.QueryRow(`SELECT id FROM tunnels WHERE engagement = ? AND listen = ? LIMIT 1`, eng.ID, in.Listen)
		var existing string
		switch err := row.Scan(&existing); {
		case err == nil:
			id = existing
			sets, args := updates(map[string]string{
				"listen": in.Listen, "via": in.Via, "reachable": in.Reachable,
				"entry_kind": in.EntryKind, "kind": in.Kind, "status": in.Status,
			}, now)
			sets = append(sets, "last_check = ?")
			args = append(args, now, id, eng.ID)
			_, err := conn.Exec(`UPDATE tunnels SET `+strings.Join(sets, ", ")+` WHERE id = ? AND engagement = ?`, args...)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		id = newID("tnl")
		_, err := conn.Exec(`INSERT INTO tunnels
			(id, engagement, kind, entry_kind, listen, via, reachable, status, last_check, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, eng.ID, in.Kind, in.EntryKind, in.Listen, in.Via, in.Reachable,
			def(in.Status, "down"), now, now, now)
		return err
	})
	if err != nil {
		return Tunnel{}, err
	}
	out, err = d.Tunnel(eng.ID, id)
	return out, err
}

// Tunnel reads one back.
func (d *DB) Tunnel(engagement, id string) (Tunnel, error) {
	var tn Tunnel
	err := d.do(func(conn *sql.DB) error {
		return conn.QueryRow(`SELECT id, engagement, kind, entry_kind, listen, via, reachable, status, last_check, created_at, updated_at
			FROM tunnels WHERE id = ? AND engagement = ?`, id, engagement).
			Scan(&tn.ID, &tn.Engagement, &tn.Kind, &tn.EntryKind, &tn.Listen, &tn.Via, &tn.Reachable,
				&tn.Status, &tn.LastCheck, &tn.CreatedAt, &tn.UpdatedAt)
	})
	return tn, err
}

// Tunnels lists an engagement's tunnels.
func (d *DB) Tunnels(engagement string) ([]Tunnel, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Tunnel
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, kind, entry_kind, listen, via, reachable, status, last_check, created_at, updated_at
			FROM tunnels WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tn Tunnel
			if err := rows.Scan(&tn.ID, &tn.Engagement, &tn.Kind, &tn.EntryKind, &tn.Listen, &tn.Via,
				&tn.Reachable, &tn.Status, &tn.LastCheck, &tn.CreatedAt, &tn.UpdatedAt); err != nil {
				return err
			}
			out = append(out, tn)
		}
		return rows.Err()
	})
	return out, err
}

// Webshell is a shell on an asset, and whether the agent really reached it
// (the skills' "WebShell 管理端必须实测用户能连上").
type Webshell struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Target     string `json:"target,omitempty"`
	URL        string `json:"url,omitempty"`
	ShellType  string `json:"shellType,omitempty"`
	PassKey    string `json:"passKey,omitempty"`
	Privilege  string `json:"privilege,omitempty"`
	Verified   bool   `json:"verified,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// WebshellIn is what a tool passes to record a shell.
type WebshellIn struct {
	Target    string
	URL       string
	ShellType string
	PassKey   string
	Privilege string
	Verified  *bool
}

// AddWebshell records a shell, or amends the one at that URL.
func (d *DB) AddWebshell(engagement string, in WebshellIn) (Webshell, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Webshell{}, err
	}
	if in.URL == "" {
		return Webshell{}, errors.New("a webshell needs its url")
	}
	var out Webshell
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		sets, args := updates(map[string]string{
			"target": in.Target, "shell_type": in.ShellType, "pass_key": in.PassKey, "privilege": in.Privilege,
		}, now)
		if in.Verified != nil {
			sets = append(sets, "verified = ?")
			args = append(args, boolInt(*in.Verified))
		}
		args = append(args, eng.ID, in.URL)
		res, err := conn.Exec(`UPDATE webshells SET `+strings.Join(sets, ", ")+` WHERE engagement = ? AND url = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		_, err = conn.Exec(`INSERT INTO webshells
			(id, engagement, target, url, shell_type, pass_key, privilege, verified, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			newID("wsh"), eng.ID, in.Target, in.URL, in.ShellType, in.PassKey, in.Privilege,
			boolIntPtr(in.Verified), now, now)
		return err
	})
	if err != nil {
		return Webshell{}, err
	}
	out, err = d.webshell(eng.ID, in.URL)
	return out, err
}

// webshell reads back the row at url.
func (d *DB) webshell(engagement, url string) (Webshell, error) {
	var w Webshell
	err := d.do(func(conn *sql.DB) error {
		var verified int
		if err := conn.QueryRow(`SELECT id, engagement, target, url, shell_type, pass_key, privilege, verified, created_at, updated_at
			FROM webshells WHERE engagement = ? AND url = ? ORDER BY created_at LIMIT 1`, engagement, url).
			Scan(&w.ID, &w.Engagement, &w.Target, &w.URL, &w.ShellType, &w.PassKey, &w.Privilege,
				&verified, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return err
		}
		w.Verified = verified != 0
		return nil
	})
	return w, err
}

// Webshells lists an engagement's shells.
func (d *DB) Webshells(engagement string) ([]Webshell, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Webshell
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, url, shell_type, pass_key, privilege, verified, created_at, updated_at
			FROM webshells WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w Webshell
			var verified int
			if err := rows.Scan(&w.ID, &w.Engagement, &w.Target, &w.URL, &w.ShellType, &w.PassKey,
				&w.Privilege, &verified, &w.CreatedAt, &w.UpdatedAt); err != nil {
				return err
			}
			w.Verified = verified != 0
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// Session is a channel to a host: an interactive shell, a forward, a
// webshell's own session.
type Session struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Target     string `json:"target,omitempty"`
	Name       string `json:"name,omitempty"`
	How        string `json:"how,omitempty"`
	Status     string `json:"status,omitempty"`
	LastCheck  int64  `json:"lastCheck,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// AddSession records a session, or checks the one with that name.
func (d *DB) AddSession(engagement, target, name, how string) (Session, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = newID("ses")
	}
	now := Now()
	var out Session
	err = d.do(func(conn *sql.DB) error {
		res, err := conn.Exec(`UPDATE sessions SET last_check = ?, status = 'live', target = COALESCE(NULLIF(?,''), target), how = COALESCE(NULLIF(?,''), how)
			WHERE engagement = ? AND name = ?`, now, target, how, eng.ID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			_, err = conn.Exec(`INSERT INTO sessions (id, engagement, target, name, how, status, last_check, created_at)
				VALUES (?,?,?,?,?,?,?,?)`, newID("ses"), eng.ID, target, name, how, "live", now, now)
		}
		return err
	})
	if err != nil {
		return Session{}, err
	}
	err = d.do(func(conn *sql.DB) error {
		return conn.QueryRow(`SELECT id, engagement, target, name, how, status, last_check, created_at
			FROM sessions WHERE engagement = ? AND name = ?`, eng.ID, name).
			Scan(&out.ID, &out.Engagement, &out.Target, &out.Name, &out.How, &out.Status, &out.LastCheck, &out.CreatedAt)
	})
	return out, err
}

// Sessions lists an engagement's sessions.
func (d *DB) Sessions(engagement string) ([]Session, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Session
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, name, how, status, last_check, created_at
			FROM sessions WHERE engagement = ? ORDER BY last_check DESC`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s Session
			if err := rows.Scan(&s.ID, &s.Engagement, &s.Target, &s.Name, &s.How, &s.Status,
				&s.LastCheck, &s.CreatedAt); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}

// ---------------------------------------------------------------------
// POCs, scores, chains, evidence, attack files
// ---------------------------------------------------------------------

// Poc is a technique that worked, kept past the engagement it was learned
// in: the knowledge base a later engagement searches first.
type Poc struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category,omitempty"`
	Engagement   string `json:"engagement,omitempty"`
	AssetTarget  string `json:"assetTarget,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Content      string `json:"content,omitempty"`
	VerifiedNote string `json:"verifiedNote,omitempty"`
	Verified     bool   `json:"verified,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// PocIn is what a tool passes to keep a technique.
type PocIn struct {
	Name         string
	Category     string
	Engagement   string
	AssetTarget  string
	Severity     string
	Content      string
	VerifiedNote string
	Verified     *bool
}

// AddPoc keeps a technique by name: the same name again amends it, since a
// second engagement verifying it adds a note rather than a second row.
func (d *DB) AddPoc(in PocIn) (Poc, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Poc{}, errors.New("a POC needs a name")
	}
	eng := ""
	if in.Engagement != "" {
		e, err := d.Target(in.Engagement)
		if err != nil {
			return Poc{}, err
		}
		eng = e.ID
	}
	var out Poc
	err := d.do(func(conn *sql.DB) error {
		now := Now()
		sets, args := updates(map[string]string{
			"category": in.Category, "content": in.Content, "verified_note": in.VerifiedNote,
			"asset_target": in.AssetTarget,
		}, now)
		if in.Verified != nil {
			sets = append(sets, "verified = ?")
			args = append(args, boolInt(*in.Verified))
		}
		if eng != "" {
			sets = append(sets, "engagement = ?")
			args = append(args, eng)
		}
		args = append(args, name)
		res, err := conn.Exec(`UPDATE pocs SET `+strings.Join(sets, ", ")+` WHERE name = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		_, err = conn.Exec(`INSERT INTO pocs
			(id, name, category, engagement, asset_target, severity, content, verified_note, verified, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			newID("poc"), name, in.Category, eng, in.AssetTarget, in.Severity, in.Content,
			in.VerifiedNote, boolIntPtr(in.Verified), now, now)
		return err
	})
	if err != nil {
		return Poc{}, err
	}
	out, err = d.PocByName(name)
	return out, err
}

// PocByName reads a technique by its name.
func (d *DB) PocByName(name string) (Poc, error) {
	var p Poc
	err := d.do(func(conn *sql.DB) error {
		var verified int
		if err := conn.QueryRow(`SELECT id, name, category, engagement, asset_target, severity, content, verified_note, verified, created_at, updated_at
			FROM pocs WHERE name = ?`, name).
			Scan(&p.ID, &p.Name, &p.Category, &p.Engagement, &p.AssetTarget, &p.Severity, &p.Content,
				&p.VerifiedNote, &verified, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return err
		}
		p.Verified = verified != 0
		return nil
	})
	return p, err
}

// SearchPoc finds techniques by a word in their name, category or content.
// Every engagement's are searched, not only the caller's: that is the
// point of keeping them.
func (d *DB) SearchPoc(_, query string) ([]Poc, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Poc
	err := d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, name, category, engagement, asset_target, severity, content, verified_note, verified, created_at, updated_at
			FROM pocs ORDER BY verified DESC, name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p Poc
			var verified int
			if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Engagement, &p.AssetTarget, &p.Severity,
				&p.Content, &p.VerifiedNote, &verified, &p.CreatedAt, &p.UpdatedAt); err != nil {
				return err
			}
			p.Verified = verified != 0
			if q != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Category+" "+p.Content+" "+p.VerifiedNote), q) {
				continue
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// Score is one point claimed, and whether it was already counted.
type Score struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Category   string `json:"category"`
	Target     string `json:"target"`
	Points     int    `json:"points"`
	AssetID    string `json:"assetId,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	// Already says a score for this category and target was there before:
	// a host taken twice is worth its points once.
	Already bool `json:"already"`
}

// Score counts a hit once per category and target.
func (d *DB) Score(engagement, category, target string, points int, evidence string) (Score, error) {
	category = strings.TrimSpace(category)
	target = strings.TrimSpace(target)
	if category == "" || target == "" {
		return Score{}, errors.New("a score needs a category and a target (host:port)")
	}
	eng, err := d.Target(engagement)
	if err != nil {
		return Score{}, err
	}
	var out Score
	err = d.do(func(conn *sql.DB) error {
		now := Now()
		var id string
		var counted int
		err := conn.QueryRow(`SELECT id, points FROM scores WHERE engagement = ? AND category = ? AND target = ?`,
			eng.ID, category, target).Scan(&id, &counted)
		switch {
		case err == nil:
			out = Score{ID: id, Engagement: eng.ID, Category: category, Target: target,
				Points: counted, Evidence: evidence, Already: true}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		id = newID("scr")
		host, port := hostPort(target)
		assetID, _ := d.assetIDFor(conn, host, port)
		_, err = conn.Exec(`INSERT INTO scores (id, engagement, category, target, points, asset_id, evidence, created_at)
			VALUES (?,?,?,?,?,?,?,?)`, id, eng.ID, category, target, points, assetID, evidence, now)
		if err != nil {
			return err
		}
		out = Score{ID: id, Engagement: eng.ID, Category: category, Target: target, Points: points,
			AssetID: assetID, Evidence: evidence, CreatedAt: now}
		return nil
	})
	return out, err
}

// assetIDFor is the asset a scored target's host and port belong to.
func (d *DB) assetIDFor(conn *sql.DB, host string, port int) (string, error) {
	var id string
	err := conn.QueryRow(`SELECT id FROM assets WHERE ip = ? AND port = ? ORDER BY created_at LIMIT 1`, host, port).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// Scores lists an engagement's points.
func (d *DB) Scores(engagement string) ([]Score, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Score
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, category, target, points, asset_id, evidence, created_at
			FROM scores WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s Score
			if err := rows.Scan(&s.ID, &s.Engagement, &s.Category, &s.Target, &s.Points,
				&s.AssetID, &s.Evidence, &s.CreatedAt); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}

// Chain is one action: the command as it was run, and what came back.
type Chain struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	StageCode  string `json:"stageCode,omitempty"`
	Role       string `json:"role,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Result     string `json:"result,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// AddChain records one action of an engagement.
func (d *DB) AddChain(engagement, stage, role, tool, result string) (Chain, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Chain{}, err
	}
	if strings.TrimSpace(tool) == "" && strings.TrimSpace(result) == "" {
		return Chain{}, errors.New("an action needs the command (tool) or its result")
	}
	c := Chain{ID: newID("chn"), Engagement: eng.ID, StageCode: stage, Role: role, Tool: tool, Result: result, CreatedAt: Now()}
	err = d.do(func(conn *sql.DB) error {
		_, err := conn.Exec(`INSERT INTO chains (id, engagement, stage_code, role, tool, result, created_at)
			VALUES (?,?,?,?,?,?,?)`, c.ID, c.Engagement, c.StageCode, c.Role, c.Tool, c.Result, c.CreatedAt)
		return err
	})
	return c, err
}

// Chains lists an engagement's actions, in the order they were taken.
func (d *DB) Chains(engagement string) ([]Chain, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Chain
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, stage_code, role, tool, result, created_at
			FROM chains WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Chain
			if err := rows.Scan(&c.ID, &c.Engagement, &c.StageCode, &c.Role, &c.Tool, &c.Result, &c.CreatedAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// Evidence is a raw request and the response it got: the proof a finding
// rests on.
type Evidence struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Target     string `json:"target,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Request    string `json:"request,omitempty"`
	Response   string `json:"response,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// AddEvidence keeps a request and its response.
func (d *DB) AddEvidence(engagement, target, kind, request, response string) (Evidence, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Evidence{}, err
	}
	if request == "" && response == "" {
		return Evidence{}, errors.New("evidence needs the request or the response")
	}
	e := Evidence{ID: newID("evd"), Engagement: eng.ID, Target: target, Kind: def(kind, "http"),
		Request: request, Response: response, CreatedAt: Now()}
	err = d.do(func(conn *sql.DB) error {
		_, err := conn.Exec(`INSERT INTO evidence (id, engagement, target, kind, request, response, created_at)
			VALUES (?,?,?,?,?,?,?)`, e.ID, e.Engagement, e.Target, e.Kind, e.Request, e.Response, e.CreatedAt)
		return err
	})
	return e, err
}

// Evidences lists an engagement's proofs.
func (d *DB) Evidences(engagement string) ([]Evidence, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []Evidence
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, target, kind, request, response, created_at
			FROM evidence WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Evidence
			if err := rows.Scan(&e.ID, &e.Engagement, &e.Target, &e.Kind, &e.Request, &e.Response, &e.CreatedAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// AttackFile is a file built for an engagement (a payload, a shell, a
// dump) and where the agent left it.
type AttackFile struct {
	ID         string `json:"id"`
	Engagement string `json:"engagement"`
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Delivered  string `json:"delivered,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

// AddAttackFile records a file built or delivered.
func (d *DB) AddAttackFile(engagement, name, path, purpose, delivered string) (AttackFile, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return AttackFile{}, err
	}
	if strings.TrimSpace(name) == "" {
		return AttackFile{}, errors.New("an attack file needs a name")
	}
	a := AttackFile{ID: newID("atk"), Engagement: eng.ID, Name: name, Path: path, Purpose: purpose,
		Delivered: delivered, CreatedAt: Now()}
	err = d.do(func(conn *sql.DB) error {
		_, err := conn.Exec(`INSERT INTO attack_files (id, engagement, name, path, purpose, delivered, created_at)
			VALUES (?,?,?,?,?,?,?)`, a.ID, a.Engagement, a.Name, a.Path, a.Purpose, a.Delivered, a.CreatedAt)
		return err
	})
	return a, err
}

// AttackFiles lists an engagement's files.
func (d *DB) AttackFiles(engagement string) ([]AttackFile, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return nil, err
	}
	var out []AttackFile
	err = d.do(func(conn *sql.DB) error {
		rows, err := conn.Query(`SELECT id, engagement, name, path, purpose, delivered, created_at
			FROM attack_files WHERE engagement = ? ORDER BY created_at`, eng.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a AttackFile
			if err := rows.Scan(&a.ID, &a.Engagement, &a.Name, &a.Path, &a.Purpose, &a.Delivered, &a.CreatedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// ---------------------------------------------------------------------
// the report
// ---------------------------------------------------------------------

// Report is what an engagement comes to: the commander reads it to check a
// subagent's claim, and the window's page shows it.
type Report struct {
	Engagement  Engagement   `json:"engagement"`
	Assets      int          `json:"assets"`
	Internet    int          `json:"internet"`
	Internal    int          `json:"internal"`
	Vulns       int          `json:"vulns"`
	Criticals   int          `json:"criticals"`
	Scores      int          `json:"scores"`
	ScoreRows   int          `json:"scoreRows"`
	Chains      int          `json:"chains"`
	Credentials int          `json:"credentials"`
	TunnelsUp   int          `json:"tunnelsUp"`
	Webshells   int          `json:"webshells"`
	Sessions    int          `json:"sessions"`
	Pocs        int          `json:"pocs"`
	Recent      []Chain      `json:"recent,omitempty"`
	TopScores   []Score      `json:"topScores,omitempty"`
	AssetsList  []Asset      `json:"assetsList,omitempty"`
	VulnsList   []Vuln       `json:"vulnsList,omitempty"`
	TunnelList  []Tunnel     `json:"tunnelList,omitempty"`
	ShellList   []Webshell   `json:"shellList,omitempty"`
	EntryList   []Entry      `json:"entryList,omitempty"`
	SessionList []Session    `json:"sessionList,omitempty"`
	PocList     []Poc        `json:"pocList,omitempty"`
	FileList    []AttackFile `json:"fileList,omitempty"`
}

// Report rolls an engagement up, with the lists a report needs.
func (d *DB) Report(engagement string) (Report, error) {
	eng, err := d.Target(engagement)
	if err != nil {
		return Report{}, err
	}
	r := Report{Engagement: eng}
	if r.AssetsList, err = d.Assets(eng.ID); err != nil {
		return Report{}, err
	}
	r.Assets = len(r.AssetsList)
	for _, a := range r.AssetsList {
		switch a.Scope {
		case "internal":
			r.Internal++
		case "internet":
			r.Internet++
		}
	}
	if r.VulnsList, err = d.Vulns(eng.ID); err != nil {
		return Report{}, err
	}
	r.Vulns = len(r.VulnsList)
	for _, v := range r.VulnsList {
		if v.Severity == "critical" {
			r.Criticals++
		}
	}
	if scores, err := d.Scores(eng.ID); err == nil {
		r.ScoreRows = len(scores)
		for _, s := range scores {
			r.Scores += s.Points
		}
		r.TopScores = scores
	} else {
		return Report{}, err
	}
	if chains, err := d.Chains(eng.ID); err == nil {
		r.Chains = len(chains)
		if len(chains) > 10 {
			chains = chains[len(chains)-10:]
		}
		r.Recent = chains
	} else {
		return Report{}, err
	}
	if creds, err := d.Credentials(eng.ID, false); err == nil {
		r.Credentials = len(creds)
	} else {
		return Report{}, err
	}
	if r.TunnelList, err = d.Tunnels(eng.ID); err != nil {
		return Report{}, err
	}
	for _, t := range r.TunnelList {
		if t.Status == "up" {
			r.TunnelsUp++
		}
	}
	if r.ShellList, err = d.Webshells(eng.ID); err != nil {
		return Report{}, err
	}
	r.Webshells = len(r.ShellList)
	if r.EntryList, err = d.Entries(eng.ID); err != nil {
		return Report{}, err
	}
	if r.SessionList, err = d.Sessions(eng.ID); err != nil {
		return Report{}, err
	}
	r.Sessions = len(r.SessionList)
	if r.PocList, err = d.SearchPoc(eng.ID, ""); err != nil {
		return Report{}, err
	}
	r.Pocs = len(r.PocList)
	if r.FileList, err = d.AttackFiles(eng.ID); err != nil {
		return Report{}, err
	}
	return r, nil
}
