// Package proxypool is a provider's pool of proxies: several exits for one
// vendor, tried in turn, each taken out of the rotation while it fails and
// put back when it answers again (#237 keeps one proxy per provider or per
// account; a pool is the several-exits case, e.g. a vendor met from
// different addresses so its limits are not all spent from one).
//
// A pool belongs to a provider (providers.json, ProxyPool) and is picked
// from per request: the provider's Proxy keeps naming one address, a
// caller's own header still pins a request to one, and an exit the caller
// pinned is remembered for that caller's session (sticky), so a vendor's
// session affinity survives rotation.
//
// The pool's health is this process's, not the file's: a restart forgets
// which exit was resting. It is deliberately so — an exit that failed
// because of a moment is back at once after a restart, and providers.json
// stays the user's alone, never rewritten by the gateway at runtime.
//
// Where a pick is turned into a request: internal/provider's Do, which asks
// the pool and lays the choice on the request's context, the same way a
// provider's own proxy is laid there (netproxy).
package proxypool

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Exit is one proxy of the pool.
type Exit struct {
	// URL is the proxy's address, as Provider.Proxy takes one
	// (http://, https://, socks5://; host:port means http), or "direct".
	URL string `json:"url"`
	// Label is what the editor shows for it; "" for none.
	Label string `json:"label,omitempty"`
	// Weight is how often the exit is picked against the others, 0 as 1.
	Weight int `json:"weight,omitempty"`
	// Disabled keeps the exit in the pool without being picked.
	Disabled bool `json:"disabled,omitempty"`
}

// Pool is a provider's own exit pool.
type Pool struct {
	// Exits are tried by Mode; an empty pool is no pool.
	Exits []Exit `json:"exits,omitempty"`
	// Mode is how an exit is picked: "round-robin" (in order, default),
	// "random" (by weight), "least-latency" (the fastest that answered),
	// "sticky" (one exit per caller until it fails).
	Mode string `json:"mode,omitempty"`
	// FailThreshold is how many failures in a row take an exit out of the
	// rotation, 0 as 2.
	FailThreshold int `json:"failThreshold,omitempty"`
	// Rest is how long an exit taken out stays out, 0 as 60s.
	Rest string `json:"rest,omitempty"`
	// Disabled leaves the pool saved but unused: the provider's Proxy (or
	// the global one) then serves every request, as before.
	Disabled bool `json:"disabled,omitempty"`
}

// Modes are the pool's modes, as the editor offers them.
var Modes = []string{"round-robin", "random", "least-latency", "sticky"}

// On says the pool has exits and is used.
func (p Pool) On() bool { return !p.Disabled && len(p.usable()) > 0 }

// ModeOr is the pool's mode, round-robin by default.
func (p Pool) ModeOr() string {
	m := strings.TrimSpace(p.Mode)
	if m == "" {
		return Modes[0]
	}
	return m
}

// Failed is how many failures in a row take an exit out, 2 by default.
func (p Pool) Failed() int {
	if p.FailThreshold > 0 {
		return p.FailThreshold
	}
	return 2
}

// Resting is how long a taken-out exit stays out, a minute by default.
func (p Pool) Resting() time.Duration {
	if strings.TrimSpace(p.Rest) == "" {
		return time.Minute
	}
	d, err := time.ParseDuration(p.Rest)
	if err != nil || d <= 0 {
		return time.Minute
	}
	return d
}

// usable is the exits the pool may pick, disabled ones left out.
func (p Pool) usable() []Exit {
	out := make([]Exit, 0, len(p.Exits))
	for _, e := range p.Exits {
		if !e.Disabled && strings.TrimSpace(e.URL) != "" {
			out = append(out, e)
		}
	}
	return out
}

// Valid says why the pool can't be saved, nil when it can.
func (p Pool) Valid() error {
	m := p.ModeOr()
	known := false
	for _, x := range Modes {
		if x == m {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%q is no pool mode: pick one of %s", p.Mode, strings.Join(Modes, ", "))
	}
	if s := strings.TrimSpace(p.Rest); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			return fmt.Errorf("%q is no length of time: write 30s, 5m, 1h", p.Rest)
		}
	}
	if p.FailThreshold < 0 {
		return fmt.Errorf("a fail threshold is a count of failures, 0 or more")
	}
	seen := map[string]bool{}
	for _, e := range p.Exits {
		a := strings.TrimSpace(e.URL)
		if a == "" {
			return fmt.Errorf("every exit of a pool needs an address")
		}
		if a != "direct" {
			if _, err := url.Parse(a); err != nil {
				return fmt.Errorf("%q is no proxy address", a)
			}
		}
		if seen[a] {
			return fmt.Errorf("%s is in the pool twice", a)
		}
		seen[a] = true
	}
	return nil
}

// ExitState is one exit as the editor shows it: the pool's own facts and
// what this process has seen of it.
type ExitState struct {
	Exit
	// State is "ready", "resting" or "used up" (resting for longer than a
	// rest says: kept out until it is tried by hand).
	State string `json:"state"`
	// Resting is how long it stays out, when it is.
	Resting string `json:"resting,omitempty"`
	// Picks, Failures and Answered count this process's use of it.
	Picks    int64 `json:"picks"`
	Failures int64 `json:"failures"`
	Answered int64 `json:"answered"`
	// Latency is the last answering time, ms.
	Latency int64 `json:"latency,omitempty"`
	// Mean is its mean answering time, ms.
	Mean int64 `json:"mean,omitempty"`
	// Err is why it last failed.
	Err string `json:"err,omitempty"`
	// Since is when it was last picked, unix ms; 0 for never.
	Since int64 `json:"since,omitempty"`
}

// pickError marks a request that failed for a reason that moves the pool
// on: the exit's own failure, or the vendor saying "not now".
func pickError(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504, 522, 524:
		return true
	}
	return false
}

type health struct {
	picks, failures, answered int64
	latency, mean             int64 // ms
	err                       string
	since                     int64 // unix ms of the last pick
	resting                   time.Time
	round                     int
}

type state struct {
	mu sync.Mutex
	by map[string]*health // by exit address
	pin map[string]string // caller pin → exit address
}

var (
	regMu sync.Mutex
	reg   = map[string]*state{}
)

// of is the health of the pool named id, made if it isn't there yet.
func of(id string) *state {
	regMu.Lock()
	defer regMu.Unlock()
	s, ok := reg[id]
	if !ok {
		s = &state{by: map[string]*health{}, pin: map[string]string{}}
		reg[id] = s
	}
	return s
}

// Pick chooses an exit of the pool named id and returns the choice
// (netproxy.With's) and a done to report with what came back, or ok false
// when the pool has nothing to pick from — the request then goes as it
// would without a pool.
//
// pin, when it isn't empty, is the caller's own identity (an account, a
// key, a session): an exit it is already on is kept while that exit is
// answering, so a vendor's session affinity survives rotation.
func Pick(id string, p Pool, pin string) (choice string, done func(*http.Response, error), ok bool) {
	exits := p.usable()
	if !p.On() || len(exits) == 0 {
		return "", nil, false
	}
	s := of(id)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	// a caller pinned to an exit keeps it while it is ready
	if pin != "" {
		if addr, ok := s.pin[pin]; ok {
			if e, ok := find(exits, addr); ok && s.ready(e, now, p) {
				s.count(e, now)
				return e.URL, s.done(e, p, now), true
			}
		}
	}

	e, ok := s.choose(exits, p, now)
	if !ok {
		return "", nil, false
	}
	if pin != "" && (p.ModeOr() == "sticky" || p.ModeOr() == "least-latency" || p.ModeOr() == "round-robin") {
		s.pin[pin] = e.URL
	}
	s.count(e, now)
	return e.URL, s.done(e, p, now), true
}

// choose is the exit this pick takes, by the pool's mode.
func (s *state) choose(exits []Exit, p Pool, now time.Time) (Exit, bool) {
	ready := make([]Exit, 0, len(exits))
	for _, e := range exits {
		if s.ready(e, now, p) {
			ready = append(ready, e)
		}
	}
	if len(ready) == 0 {
		// everything is resting: the one that comes back soonest, so a
		// vendor is never left with no exit at all
		best, bestAt := Exit{}, time.Time{}
		for _, e := range exits {
			h := s.by[e.URL]
			at := time.Time{}
			if h != nil {
				at = h.resting
			}
			if best.URL == "" || at.Before(bestAt) {
				best, bestAt = e, at
			}
		}
		if best.URL == "" {
			return Exit{}, false
		}
		return best, true
	}
	switch p.ModeOr() {
	case "random":
		return weighted(ready), true
	case "least-latency":
		best, bestMean := ready[0], int64(-1)
		for _, e := range ready {
			h := s.by[e.URL]
			m := int64(0)
			if h != nil {
				m = h.mean
			}
			if m > 0 && (bestMean < 0 || m < bestMean) {
				best, bestMean = e, m
			}
		}
		return best, true
	case "sticky":
		// the same exit for everyone until it fails
		best := ready[0]
		for _, e := range ready {
			h := s.by[e.URL]
			if h != nil && h.answered > 0 {
				best = e
				break
			}
		}
		return best, true
	default: // round-robin
		return ready[s.roundOf(ready)%len(ready)], true
	}
}

// roundOf is where the rotation stands among exits.
func (s *state) roundOf(exits []Exit) int {
	h := s.by[exits[0].URL]
	if h == nil {
		h = &health{}
		s.by[exits[0].URL] = h
	}
	n := h.round
	h.round++
	return n
}

// ready says an exit may be picked: not resting now, or its rest is over.
func (s *state) ready(e Exit, now time.Time, p Pool) bool {
	h := s.by[e.URL]
	if h == nil {
		return true
	}
	if h.resting.IsZero() || !now.Before(h.resting) {
		if !h.resting.IsZero() {
			h.failures = 0
			h.resting = time.Time{}
		}
		return true
	}
	return false
}

func (s *state) count(e Exit, now time.Time) {
	h := s.by[e.URL]
	if h == nil {
		h = &health{}
		s.by[e.URL] = h
	}
	h.picks++
	h.since = now.UnixMilli()
}

// done reports what came back, and takes the exit out of the rotation when
// it failed or the vendor said "not now".
func (s *state) done(e Exit, p Pool, now time.Time) func(*http.Response, error) {
	return func(res *http.Response, err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		h := s.by[e.URL]
		if h == nil {
			h = &health{}
			s.by[e.URL] = h
		}
		at := time.Now()
		bad := err != nil || res != nil && pickError(res.StatusCode)
		if !bad {
			h.failures = 0
			h.resting = time.Time{}
			h.answered++
			h.latency = at.Sub(now).Milliseconds()
			if h.mean == 0 {
				h.mean = h.latency
			} else {
				h.mean = (h.mean*3 + h.latency) / 4
			}
			h.err = ""
			// the caller's pin stays on an exit that answered
			return
		}
		h.failures++
		if err != nil {
			h.err = err.Error()
		} else {
			h.err = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		if int(h.failures) >= p.Failed() {
			h.resting = at.Add(p.Resting())
		}
		// a caller pinned to a failed exit moves on the next pick
		for k, v := range s.pin {
			if v == e.URL {
				delete(s.pin, k)
			}
		}
	}
}

// find is the exit with address in a pool's usable exits.
func find(exits []Exit, addr string) (Exit, bool) {
	for _, e := range exits {
		if e.URL == addr {
			return e, true
		}
	}
	return Exit{}, false
}

// weighted picks by the exits' weights.
func weighted(exits []Exit) Exit {
	total := 0
	for _, e := range exits {
		total += max(e.Weight, 1)
	}
	n := int(rand.Uint32()) % total
	for _, e := range exits {
		n -= max(e.Weight, 1)
		if n < 0 {
			return e
		}
	}
	return exits[len(exits)-1]
}

// Snapshot is the pool as the editor shows it, the health this process
// has seen included.
func Snapshot(id string, p Pool) []ExitState {
	s := of(id)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExitState, 0, len(p.Exits))
	for _, e := range p.usable() {
		sn := ExitState{Exit: e, State: "ready"}
		if h := s.by[e.URL]; h != nil {
			sn.Picks, sn.Failures, sn.Answered = h.picks, h.failures, h.answered
			sn.Latency, sn.Mean, sn.Err, sn.Since = h.latency, h.mean, h.err, h.since
			if !h.resting.IsZero() && now.Before(h.resting) {
				sn.State = "resting"
				sn.Resting = h.resting.Sub(now).Round(time.Second).String()
			}
		}
		out = append(out, sn)
	}
	return out
}

// Rest puts an exit out of the rotation for the pool's rest, as a failure
// would; "used up" for longer than one rest, so a vendor that has banned an
// exit is left alone. The editor's own switch.
func Rest(id, addr string, d time.Duration) {
	s := of(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.by[addr]
	if h == nil {
		h = &health{}
		s.by[addr] = h
	}
	h.resting = time.Now().Add(d)
}

// Forget clears the pool named id's health, as a restart would.
func Forget(id string) {
	regMu.Lock()
	delete(reg, id)
	regMu.Unlock()
}

// ForgetAll clears every pool's health. Tests.
func ForgetAll() {
	regMu.Lock()
	reg = map[string]*state{}
	regMu.Unlock()
}
