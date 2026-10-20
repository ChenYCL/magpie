// Package gwplugin is the gateway's plugin API for programs outside magpie:
// a layer of its own that every agent's request and every usage row goes
// through, so a plugin can be written in any language and run as its own
// process, or even be another gateway.
//
// # Where it sits, and why there
//
// Behind the gateway's own door, not in front of it. A plugin is called
// once the caller's key is checked, the model resolved and the provider
// picked — never before. That is what makes it able to do the things an
// in-front proxy cannot:
//
//   - it is told the agent, the model, the provider and the session, which
//     the gateway alone knows (an in-front proxy sees a bare HTTP body);
//   - it sees the body in the agent's own API (chat, anthropic, responses,
//     gemini) as the gateway will route it, so a change it makes is a change
//     to what the vendor receives, not to what the agent sent;
//   - its failure can be told apart from the vendor's: a plugin that fails
//     leaves the request exactly as it was (fail open), while an in-front
//     proxy that dies is a broken gateway.
//
// What it deliberately cannot do: see the client's credentials, change the
// model or the stream flag (the gateway checked the caller's key against
// them), or reject a request the caller's key may make — a plugin edits a
// request, it is not the gateway's access control.
//
// # The API
//
// A plugin is an HTTP endpoint. magpie POSTs JSON to it; the plugin answers
// with JSON. Nothing else is needed: no SDK, no shared library, no language.
//
//	POST <url>/request
//	  {"protocol":"anthropic","model":"claude-sonnet-5-5",
//	   "provider":"claude","agent":"claude-code","session":"…",
//	   "path":"/v1/messages","stream":true,
//	   "body":{…the request as the vendor will get it…}}
//	  → the new body as a JSON object (200), or
//	    {"reject":{"status":400,"message":"why"}} to end the request, or
//	    {} or 204 to leave it as it was.
//
//	POST <url>/usage
//	  {"rows":[{…one usage row…}, …]}
//	  → anything; ignored. Fire and forget, for a plugin that analyses
//	    traffic (spend per agent, tools per session, the red team's
//	    findings) and wants the ledger rather than the requests.
//
// A body sent as a JSON string is read as JSON too, for a plugin written in
// a language that would rather hand back text.
//
// # Failure, and what a plugin may not do
//
// A plugin that times out, refuses the connection, answers a 5xx or hands
// back something that isn't a JSON object leaves the request as it was, and
// is counted as failed; after FailThreshold failures in a row it rests for
// Rest, and is tried again after that. A plugin is never on the request's
// critical path in the sense that matters: with every plugin failing or off,
// the gateway serves exactly what it served before any was installed.
//
// A body a plugin returns is checked before it is used: it must be a JSON
// object, and its model and stream must be the ones the request had. A
// plugin that changes either is refused (the request goes out unchanged);
// the gateway checked the caller's key against those two.
package gwplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/gwmw"
	"github.com/yetone/magpie/internal/usage"
)

// File is the store's file in magpie's config folder.
const File = "gateway-plugins.json"

// Path is the store's file.
func Path() string { return filepath.Join(appdir.Config(), "plugins.d", File) }

// Plugin is one plugin gateway: where it is, and what it is asked.
type Plugin struct {
	// ID is how the editor and the usage row name it.
	ID string `json:"id"`
	// Name is what the window shows; "" is the id.
	Name string `json:"name,omitempty"`
	// URL is where it is reached (http:// or https://, a host:port means
	// http). Its hooks are paths under it: /request and /usage.
	URL string `json:"url"`
	// Agents are the agents whose requests go through it; empty or "*" is
	// every one. An agent not in it is not asked, so a red-team plugin can
	// be left off an editor's own traffic.
	Agents []string `json:"agents,omitempty"`
	// Models are the models whose requests go through it, by id or a
	// "provider/*" prefix; empty is every one.
	Models []string `json:"models,omitempty"`
	// Usage says it is also sent the ledger's rows, for analysis.
	Usage bool `json:"usage,omitempty"`
	// Timeout is how long it has to answer /request, "1500ms" by default.
	Timeout string `json:"timeout,omitempty"`
	// FailThreshold is how many failures in a row rest it, 3 by default.
	FailThreshold int `json:"failThreshold,omitempty"`
	// Rest is how long it rests, "30s" by default.
	Rest string `json:"rest,omitempty"`
	// Disabled leaves it saved without being asked.
	Disabled bool `json:"disabled,omitempty"`
}

// Store is plugins.d/gateway-plugins.json.
type Store struct {
	Plugins []Plugin `json:"plugins,omitempty"`
}

// TimeoutOr is how long the plugin has to answer /request.
func (p Plugin) TimeoutOr() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(p.Timeout)); err == nil && d > 0 {
		return d
	}
	return 1500 * time.Millisecond
}

// Failed is how many failures in a row rest the plugin, 3 by default.
func (p Plugin) Failed() int {
	if p.FailThreshold > 0 {
		return p.FailThreshold
	}
	return 3
}

// Resting is how long a rested plugin stays out, 30s by default.
func (p Plugin) Resting() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(p.Rest)); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// Asks says the plugin is asked about a request from agent for model.
func (p Plugin) Asks(agent, model string) bool {
	if p.Disabled || strings.TrimSpace(p.URL) == "" {
		return false
	}
	if len(p.Agents) > 0 && !anyMatch(p.Agents, agent, "*") {
		return false
	}
	if len(p.Models) > 0 && !anyMatch(p.Models, model, "*") {
		return false
	}
	return true
}

// anyMatch says one of the patterns takes v: the pattern itself, "*" for
// any, or a prefix ending in "*".
func anyMatch(patterns []string, v, _ string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == "", p == "*":
			return true
		case strings.HasSuffix(p, "*") && strings.HasPrefix(strings.ToLower(v), strings.ToLower(strings.TrimSuffix(p, "*"))):
			return true
		case strings.EqualFold(p, v):
			return true
		}
	}
	return false
}

// Valid says why a plugin can't be saved, nil when it can.
func (p Plugin) Valid() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("a plugin needs an id")
	}
	if strings.ContainsAny(p.ID, " \t/\\") {
		return fmt.Errorf("a plugin's id is one word: %q", p.ID)
	}
	u := strings.TrimSpace(p.URL)
	if u == "" {
		return fmt.Errorf("%s needs the address it is reached at", p.ID)
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		if !strings.Contains(u, ":") {
			return fmt.Errorf("%s: %q is no address: write http://host:port", p.ID, u)
		}
	}
	if s := strings.TrimSpace(p.Timeout); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			return fmt.Errorf("%s: %q is no length of time", p.ID, p.Timeout)
		}
	}
	if s := strings.TrimSpace(p.Rest); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			return fmt.Errorf("%s: %q is no length of time", p.ID, p.Rest)
		}
	}
	if p.FailThreshold < 0 {
		return fmt.Errorf("%s: a fail threshold is 0 or more", p.ID)
	}
	return nil
}

// Base is the plugin's address with a scheme, and no trailing slash.
func (p Plugin) Base() string {
	u := strings.TrimRight(strings.TrimSpace(p.URL), "/")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	return u
}

// ---- the store ---------------------------------------------------------------

var (
	mu    sync.Mutex
	cache *Store
	stamp fileStamp
)

// fileStamp is the file as it was when cache was read: a file another
// process wrote (the CLI, an editor) is taken within a second, as
// plugins.yml is.
type fileStamp struct{ size int64; mod time.Time }

func stampOf() fileStamp {
	st, err := os.Stat(Path())
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: st.Size(), mod: st.ModTime()}
}

// Load is the store as it is on disk, read once and kept until Save. A file
// that can't be read is an empty store: a damaged file must not stop a
// request being served.
func Load() Store {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() Store {
	if now := stampOf(); cache != nil && now == stamp {
		return *cache
	} else if cache != nil {
		cache = nil
	}
	s := Store{}
	b, err := os.ReadFile(Path())
	if err == nil && len(b) > 0 {
		var got Store
		if json.Unmarshal(b, &got) == nil {
			s = got
		}
	}
	cache = &s
	stamp = stampOf()
	return *cache
}

// Read is the store as it is on disk now.
func Read() Store {
	mu.Lock()
	defer mu.Unlock()
	cache = nil
	return load()
}

// Save writes the store.
func Save(s Store) error {
	mu.Lock()
	defer mu.Unlock()
	return write(s)
}

func write(s Store) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	cache = &s
	stamp = stampOf()
	return nil
}

// All is every plugin, saved order.
func All() []Plugin { return Load().Plugins }

// Get is the plugin with id.
func Get(id string) (Plugin, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Plugin{}, false
}

// Put adds or replaces a plugin.
func Put(p Plugin) error {
	if err := p.Valid(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	s := load()
	for i, x := range s.Plugins {
		if x.ID == p.ID {
			s.Plugins[i] = p
			return write(s)
		}
	}
	s.Plugins = append(s.Plugins, p)
	return write(s)
}

// Remove removes a plugin.
func Remove(id string) error {
	mu.Lock()
	defer mu.Unlock()
	s := load()
	out := make([]Plugin, 0, len(s.Plugins))
	found := false
	for _, x := range s.Plugins {
		if x.ID == id {
			found = true
			Forget(id)
			continue
		}
		out = append(out, x)
	}
	if !found {
		return fmt.Errorf("no plugin called %s", id)
	}
	s.Plugins = out
	return write(s)
}

// On says at least one plugin is asked about requests: what the middleware
// registers itself by.
func On() int {
	n := 0
	for _, p := range All() {
		if !p.Disabled && strings.TrimSpace(p.URL) != "" {
			n++
		}
	}
	return n
}

// ---- calling one -------------------------------------------------------------

// reply is what a plugin answers /request with.
type reply struct {
	Body   json.RawMessage `json:"body"`
	Reject *gwmw.Reject    `json:"reject"`
}

// health is what this process has seen of one plugin.
type health struct {
	calls, failures, answered int64
	mean, last                int64 // ms
	lastErr                   string
	resting                   time.Time
}

var (
	hmu sync.Mutex
	hs  = map[string]*health{}
)

func healthOf(id string) *health {
	hmu.Lock()
	defer hmu.Unlock()
	h := hs[id]
	if h == nil {
		h = &health{}
		hs[id] = h
	}
	return h
}

// Status is one plugin as the window shows it.
type Status struct {
	Plugin
	State    string `json:"state"` // ready, resting, off
	Calls    int64  `json:"calls"`
	Answered int64  `json:"answered"`
	Failures int64  `json:"failures"`
	Mean     int64  `json:"mean,omitempty"`
	Last     int64  `json:"last,omitempty"`
	Err      string `json:"err,omitempty"`
	Resting  string `json:"resting,omitempty"`
}

// Statuses is every plugin with what this process has seen of it.
func Statuses() []Status {
	now := time.Now()
	hmu.Lock()
	defer hmu.Unlock()
	out := make([]Status, 0, len(All()))
	for _, p := range All() {
		st := Status{Plugin: p, State: "ready"}
		if p.Disabled {
			st.State = "off"
		}
		if h := hs[p.ID]; h != nil {
			st.Calls, st.Answered, st.Failures = h.calls, h.answered, h.failures
			st.Mean, st.Last, st.Err = h.mean, h.last, h.lastErr
			if !h.resting.IsZero() && now.Before(h.resting) {
				st.State = "resting"
				st.Resting = h.resting.Sub(now).Round(time.Second).String()
			}
		}
		out = append(out, st)
	}
	return out
}

// Forget clears what this process has seen of a plugin. Tests, and the
// editor's own switch.
func Forget(id string) {
	hmu.Lock()
	delete(hs, id)
	hmu.Unlock()
}

// ForgetAll clears every plugin's health. Tests.
func ForgetAll() {
	hmu.Lock()
	hs = map[string]*health{}
	hmu.Unlock()
}

// resting says the plugin is out of the rotation now.
func resting(id string) bool {
	h := healthOf(id)
	hmu.Lock()
	defer hmu.Unlock()
	return !h.resting.IsZero() && time.Now().Before(h.resting)
}

// client is one HTTP client for every plugin.
var client = &http.Client{Transport: &http.Transport{
	MaxIdleConnsPerHost: 4,
	IdleConnTimeout:     90 * time.Second,
	ResponseHeaderTimeout: 0, // the request's context bounds it
}}

// Request is what a request hook is told.
type Request struct {
	Protocol string          `json:"protocol"`
	Model    string          `json:"model"`
	Provider string          `json:"provider,omitempty"`
	Agent    string          `json:"agent,omitempty"`
	Session  string          `json:"session,omitempty"`
	Path     string          `json:"path,omitempty"`
	Stream   bool            `json:"stream"`
	Body     json.RawMessage `json:"body"`
}

// Ask asks every plugin that takes this request, in the store's order, for
// the body the vendor should get. A plugin's answer is the body the next
// plugin is asked about, so two can be composed. want is the body after
// every plugin that answered.
//
// Every failure is open: a plugin that can't be reached, is resting, times
// out, answers nothing useful or changes the model or the stream leaves the
// request as it was, and is counted.
func Ask(ctx context.Context, in Request, body []byte) []byte {
	plugins := All()
	if len(plugins) == 0 || len(body) == 0 {
		return body
	}
	out := body
	changed := false
	for _, p := range plugins {
		if !p.Asks(in.Agent, in.Model) || resting(p.ID) {
			continue
		}
		next, ok := askOne(ctx, p, in, out)
		if ok && len(next) > 0 {
			out = next
			changed = true
		}
	}
	if !changed {
		return body
	}
	return out
}

// askOne asks one plugin, and says whether its answer is the new body.
func askOne(ctx context.Context, p Plugin, in Request, body []byte) ([]byte, bool) {
	h := healthOf(p.ID)
	began := time.Now()
	hmu.Lock()
	h.calls++
	hmu.Unlock()

	// the body goes as JSON: a plugin reads it the way the vendor will
	req := in
	req.Body = json.RawMessage(body)
	payload, err := json.Marshal(req)
	if err != nil {
		missed(p, began, err)
		return nil, false
	}
	cctx, cancel := context.WithTimeout(ctx, p.TimeoutOr())
	defer cancel()
	hr, err := http.NewRequestWithContext(cctx, http.MethodPost, p.Base()+"/request", bytes.NewReader(payload))
	if err != nil {
		missed(p, began, err)
		return nil, false
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("X-Magpie-Plugin", string(in.Protocol))
	res, err := client.Do(hr)
	if err != nil {
		missed(p, began, err)
		return nil, false
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(nil, res.Body, 1<<24)); err != nil {
		missed(p, began, err)
		return nil, false
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotModified {
		answered(p, began)
		return nil, false
	}
	if res.StatusCode >= 400 {
		missed(p, began, fmt.Errorf("answered %d", res.StatusCode))
		return nil, false
	}
	next, ok := readAnswer(buf.Bytes(), in, body)
	if !ok {
		missed(p, began, errors.New("answered no body magpie can use"))
		return nil, false
	}
	answered(p, began)
	return next, next != nil
}

// readAnswer is a plugin's answer as the new body: nil (leave it) for an
// empty answer, and ok false for one magpie refuses. A body is refused when
// it isn't a JSON object, or when it changes the model or the stream flag:
// the gateway checked the caller's key against those two.
func readAnswer(answer []byte, in Request, body []byte) ([]byte, bool) {
	answer = bytes.TrimSpace(answer)
	if len(answer) == 0 {
		return nil, true
	}
	if answer[0] == '"' {
		// a body handed back as a JSON string
		var s string
		if json.Unmarshal(answer, &s) == nil {
			answer = []byte(s)
		}
	}
	var raw json.RawMessage
	if json.Unmarshal(answer, &raw) != nil {
		return nil, true // not JSON at all: nothing to use, and not a failure
	}
	var wrapper reply
	if json.Unmarshal(answer, &wrapper) == nil && wrapper.Reject != nil {
		return nil, true
	}
	var obj map[string]any
	if json.Unmarshal(answer, &obj) != nil {
		return nil, false
	}
	if len(obj) == 0 {
		return nil, true
	}
	// the two the gateway keeps: the model the caller's key was checked
	// against, and whether the reply streams as the client asked
	var was map[string]any
	if json.Unmarshal(body, &was) == nil {
		if m, ok := was["model"].(string); ok {
			if got, ok := obj["model"].(string); ok && got != m {
				return nil, false
			}
		}
		if s, ok := was["stream"].(bool); ok {
			if got, ok := obj["stream"].(bool); ok && got != s {
				return nil, false
			}
		}
	}
	out, err := marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// marshal is a body as JSON with the text as it was, not escaped for HTML:
// a prompt's <tags> are the same bytes they came in as.
func marshal(obj map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// answered counts a plugin's answer.
func answered(p Plugin, began time.Time) {
	h := healthOf(p.ID)
	ms := time.Since(began).Milliseconds()
	hmu.Lock()
	h.answered++
	h.failures = 0
	h.resting = time.Time{}
	h.last = ms
	if h.mean == 0 {
		h.mean = ms
	} else {
		h.mean = (h.mean*3 + ms) / 4
	}
	h.lastErr = ""
	hmu.Unlock()
}

// missed counts a failure, and rests the plugin when they come in a row.
func missed(p Plugin, began time.Time, err error) {
	h := healthOf(p.ID)
	hmu.Lock()
	node := h
	node.failures++
	node.last = time.Since(began).Milliseconds()
	if err != nil {
		node.lastErr = err.Error()
	}
	if int(node.failures) >= p.Failed() {
		node.resting = time.Now().Add(p.Resting())
	}
	hmu.Unlock()
}

// ---- the ledger --------------------------------------------------------------

// ledgerKeep is how many rows wait for a worker before the oldest are
// dropped: an analysis plugin must never hold a request up, so a plugin
// slower than the traffic loses rows rather than memory.
const ledgerKeep = 1024

var (
	lmu     sync.Mutex
	lqueue  []usage.Record
	lwake   = make(chan struct{}, 1)
	lrun    int
	lstop   chan struct{}
	lcancel context.CancelFunc
)

// StartLedger starts the worker that sends queued rows to the plugins that
// asked for them. Called while the gateway serves.
func StartLedger() {
	lmu.Lock()
	defer lmu.Unlock()
	if lrun > 0 {
		lrun++
		return
	}
	var ctx context.Context
	ctx, lcancel = context.WithCancel(context.Background())
	lstop = make(chan struct{})
	lrun = 1
	go worker(ctx, lstop)
}

// StopLedger stops it, after the row in hand.
func StopLedger() {
	lmu.Lock()
	if lrun == 0 {
		lmu.Unlock()
		return
	}
	lrun--
	if lrun > 0 {
		lmu.Unlock()
		return
	}
	stop, cancel := lstop, lcancel
	lmu.Unlock()
	cancel()
	close(stop)
}

// Send queues a row for the plugins that asked for the ledger.
func Send(r usage.Record) {
	if !anyUsage() {
		return
	}
	lmu.Lock()
	if len(lqueue) >= ledgerKeep {
		// the oldest goes, so the newest is what an analysis plugin sees
		lqueue = lqueue[1:]
	}
	lqueue = append(lqueue, r)
	lmu.Unlock()
	select {
	case lwake <- struct{}{}:
	default:
	}
}

// anyUsage says a plugin asked for the ledger.
func anyUsage() bool {
	for _, p := range All() {
		if p.Usage && !p.Disabled && strings.TrimSpace(p.URL) != "" {
			return true
		}
	}
	return false
}

// worker sends queued rows, a batch at a time.
func worker(ctx context.Context, stop chan struct{}) {
	defer func() {
		lmu.Lock()
		lqueue = nil
		lmu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lwake:
		}
		for {
			lmu.Lock()
			batch := lqueue
			lqueue = nil
			lmu.Unlock()
			if len(batch) == 0 {
				break
			}
			sendBatch(ctx, batch)
		}
		select {
		case <-stop:
			return
		default:
		}
	}
}

// sendBatch posts one batch to each plugin that asked for the ledger.
func sendBatch(ctx context.Context, rows []usage.Record) {
	payload, err := json.Marshal(map[string]any{"rows": rows})
	if err != nil {
		return
	}
	for _, p := range All() {
		if !p.Usage || p.Disabled || strings.TrimSpace(p.URL) == "" || resting(p.ID) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, p.TimeoutOr())
		req, err := http.NewRequestWithContext(cctx, http.MethodPost, p.Base()+"/usage", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			began := time.Now()
			res, err := client.Do(req)
			if err != nil {
				missed(p, began, err)
			} else {
				io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
				res.Body.Close()
				if res.StatusCode >= 400 {
					missed(p, began, fmt.Errorf("answered %d", res.StatusCode))
				} else {
					answered(p, began)
				}
			}
		}
		cancel()
	}
}

// Example is the plugin a new one starts from, as the editor shows it: a
// program of any language that answers the two hooks, and an example body.
func Example() map[string]any {
	return map[string]any{
		"request": map[string]any{
			"protocol": "anthropic",
			"model":    "claude-sonnet-5-5",
			"provider": "claude",
			"agent":    "claude-code",
			"session":  "s-1",
			"path":     "/v1/messages",
			"stream":   true,
			"body": map[string]any{
				"model":      "claude-sonnet-5-5",
				"max_tokens": 256,
				"system":     "You are a coding agent.",
				"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
			},
		},
		"usage": map[string]any{
			"rows": []any{map[string]any{
				"time": 1792478400000, "agent": "claude-code", "provider": "claude",
				"model": "claude-sonnet-5-5", "status": 200, "input": 1244, "output": 38,
			}},
		},
	}
}

// Try asks one plugin, as the gateway would, and returns the body it gives:
// the editor's own test, so a plugin is known to work before an agent's
// traffic goes through it. A plugin that answers nothing returns the body
// as it was.
func Try(ctx context.Context, p Plugin, in Request, body []byte) ([]byte, error) {
	if err := p.Valid(); err != nil {
		return nil, err
	}
	out, ok := askOne(ctx, p, in, body)
	if !ok || len(out) == 0 {
		return body, nil
	}
	return out, nil
}

// StatusOf is one plugin's health, for the editor's row.
func StatusOf(id string) (Status, bool) {
	for _, s := range Statuses() {
		if s.ID == id {
			return s, true
		}
	}
	return Status{}, false
}
