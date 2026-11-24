// Package modelprobe keeps a standing check of which of magpie's models
// answer: every half hour, by default, each exposed model of each connected
// provider is sent the smallest request that tells a model from a dead one,
// and what came back is kept to be read in the window.
//
// It exists because a model id is not one thing: the same id is served by
// several providers, any of them may be out of quota, off, or gone, and
// telling someone which ids they may use was a matter of trying them by
// hand. The window merges the rows by id — one line per id, its providers
// behind it — and the rows are also what a page hands to a colleague.
//
// Nothing here writes a request to a vendor's account beyond what the
// editor's own Test writes (provider.TestModels, written down as magpie's
// own probe in the request log): a probe is not usage, and is counted in no
// total. A plan that is off sends nothing at all.
package modelprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
)

// File is the plan, beside the other files magpie keeps for itself.
const File = "modelprobe.json"

// ResultsFile is what the last rounds found, so a restart does not show the
// page empty while the first round of the new process runs.
const ResultsFile = "modelprobe-results.json"

// DefaultEvery is how often a round runs when the plan doesn't say.
const DefaultEvery = 30 * time.Minute

// shortestEvery keeps a plan from asking for a round more than once a
// minute: a probe is a request to a vendor, and a round of a thousand
// models is a thousand of them.
const shortestEvery = time.Minute

// Plan is what the user set.
type Plan struct {
	// On: rounds run on their own. Off sends nothing; a manual run still
	// works, so a plan turned off is not a page that cannot check.
	On bool `json:"on"`
	// Every is how often a round runs, as a length of time ("30m"). Empty
	// is DefaultEvery.
	Every string `json:"every,omitempty"`
	// Models limits rounds to these ids; empty probes every exposed model.
	Models []string `json:"models,omitempty"`
}

// Normalize settles the plan's own fields: a length of time, a floor, and
// ids with no spaces around them.
func (p *Plan) Normalize() {
	p.Every = strings.TrimSpace(p.Every)
	if d, err := time.ParseDuration(p.Every); err != nil || d < shortestEvery {
		p.Every = ""
	}
	var ids []string
	for _, m := range p.Models {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(ids, m) {
			ids = append(ids, m)
		}
	}
	p.Models = ids
}

// Every is the plan's interval, DefaultEvery when it says nothing.
func (p Plan) EveryDuration() time.Duration {
	d, err := time.ParseDuration(p.Every)
	if err != nil || d < shortestEvery {
		return DefaultEvery
	}
	return d
}

// Status is one model at one provider, as the last round found it.
type Status struct {
	Provider string `json:"provider"`
	Name     string `json:"name,omitempty"` // the provider's name, as the window shows it
	Model    string `json:"model"`          // the id it is asked by at that provider
	OK       bool   `json:"ok"`
	Code     int    `json:"code,omitempty"`
	Millis   int64  `json:"ms,omitempty"`
	Error    string `json:"error,omitempty"`
	At       int64  `json:"at,omitempty"` // unix milliseconds, when it was asked
}

// Row is one model id, merged: every provider that serves it, and how each
// answered.
type Row struct {
	ID        string   `json:"id"`
	Providers []Status `json:"providers"`
	// OK is how many of its providers answered, N how many serve it, so a
	// line reads "3 of 4" without the window counting.
	OK int `json:"ok"`
	N  int `json:"n"`
	// At is the newest answer among its providers.
	At int64 `json:"at,omitempty"`
}

// Results is a round's findings, merged by model id in the order the
// catalog lists them.
type Results struct {
	Rows []Row `json:"rows"`
	// At is when the round began; Running is what the page shows while one
	// is under way, and Step is what it is at.
	At      int64  `json:"at,omitempty"`
	Running bool   `json:"running,omitempty"`
	Step    string `json:"step,omitempty"`
	// Err is a round that could not be run at all, "" when it ran.
	Err string `json:"err,omitempty"`
}

// DoD is what a running round is asked to do.
type DoD struct {
	// Providers limits the round to these ids; empty is every one that is
	// on and serves models.
	Providers []string
	// Models limits it to these model ids; empty is the plan's, or every
	// exposed model.
	Models []string
}

var (
	mu      sync.Mutex
	current Results
	// asking is set while a round runs: a second round waits rather than
	// asking the same vendor twice over.
	asking bool
	stop   context.CancelFunc
)

// Path is the plan's file.
func Path() string { return filepath.Join(appdir.Config(), File) }

// ResultsPath is the last round's findings.
func ResultsPath() string { return filepath.Join(appdir.Config(), ResultsFile) }

// Load reads the plan: the file's, or one that is off with the default
// interval. A file that doesn't parse is an error rather than a silent
// default, as elsewhere in magpie.
func Load() (Plan, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return Plan{}, nil
	}
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return Plan{}, fmt.Errorf("%s: %w", Path(), err)
	}
	p.Normalize()
	return p, nil
}

// Save writes the plan, replacing the file in one step so a reader never
// sees half of it.
func Save(p Plan) error {
	p.Normalize()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Last is what the last round found, and whether one runs now.
func Last() Results {
	mu.Lock()
	defer mu.Unlock()
	if current.Rows == nil && current.At == 0 {
		loadResults()
	}
	out := current
	out.Running = asking
	return out
}

// loadResults reads the last round's findings off disk once, so the page
// has something to show before this process's first round. Called with mu
// held.
func loadResults() {
	b, err := os.ReadFile(ResultsPath())
	if err != nil {
		return
	}
	var r Results
	if json.Unmarshal(b, &r) != nil {
		return
	}
	if r.Rows == nil {
		r.Rows = []Row{}
	}
	current.Rows, current.At = r.Rows, r.At
}

// saveResults keeps a round's findings for the next process. Called with
// mu held.
func saveResults() {
	if err := os.MkdirAll(filepath.Dir(ResultsPath()), 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(Results{Rows: current.Rows, At: current.At}, "", "  ")
	if err != nil {
		return
	}
	tmp := ResultsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, ResultsPath()); err != nil {
		os.Remove(tmp)
	}
}

// ErrRunning is what a run answers while one is under way.
var ErrRunning = errors.New("a round is already running")

// Run asks every model of every provider in what, and keeps what came back.
// It runs in the caller's goroutine: the page's manual run waits for it,
// and the standing round calls it from its own.
func Run(ctx context.Context, what DoD) (Results, error) {
	mu.Lock()
	if asking {
		mu.Unlock()
		return Results{}, ErrRunning
	}
	asking = true
	mu.Unlock()
	defer func() {
		mu.Lock()
		asking = false
		mu.Unlock()
	}()

	plan, err := Load()
	if err != nil {
		// a plan that doesn't parse must not stop a manual run
		plan = Plan{}
	}
	models := what.Models
	if len(models) == 0 {
		models = plan.Models
	}
	targets := targets(what.Providers, models)

	// whatever the round finds replaces what was known of those models,
	// and what wasn't asked this time stands as it was
	mu.Lock()
	if current.Rows == nil {
		loadResults()
	}
	found := map[string][]Status{}
	for _, r := range current.Rows {
		found[r.ID] = append([]Status(nil), r.Providers...)
	}
	mu.Unlock()

	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return Last(), err
		}
		setStep(t.p.Name + " · " + fmt.Sprint(len(t.models)) + " models")
		res := t.p.TestModels(ctx, t.models)
		at := time.Now().UnixMilli()
		for _, r := range res {
			id := r.Model
			if id == "" {
				continue
			}
			found[id] = putStatus(found[id], Status{
				Provider: t.p.ID, Name: t.p.Name, Model: id,
				OK: r.OK, Code: r.Status, Millis: r.Millis, Error: r.Error, At: at,
			})
		}
	}

	out := merge(found)
	out.At = time.Now().UnixMilli()
	mu.Lock()
	current.Rows, current.At = out.Rows, out.At
	saveResults()
	mu.Unlock()
	return out, nil
}

// putStatus replaces this provider's answer for a model, keeping the
// others' — a round that asked one provider again does not forget the rest.
func putStatus(list []Status, s Status) []Status {
	for i := range list {
		if list[i].Provider == s.Provider {
			list[i] = s
			return list
		}
	}
	return append(list, s)
}

// merge groups provider answers by model id, the catalog's order first and
// ids it doesn't know after them by name, so two rounds show their rows in
// the same places.
func merge(found map[string][]Status) Results {
	order := map[string]int{}
	for i, e := range provider.Catalog() {
		if _, ok := order[e.Model]; !ok && e.Model != "" {
			order[e.Model] = i
		}
		if _, ok := order[e.ID]; !ok && e.Group == "" && e.Model != "" {
			order[e.Model] = i
		}
	}
	rows := make([]Row, 0, len(found))
	for id, list := range found {
		sort.Slice(list, func(i, j int) bool { return list[i].Provider < list[j].Provider })
		r := Row{ID: id, Providers: list, N: len(list)}
		for _, s := range list {
			if s.OK {
				r.OK++
			}
			if s.At > r.At {
				r.At = s.At
			}
		}
		if r.ID == "" {
			continue
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, iok := order[rows[i].ID]
		oj, jok := order[rows[j].ID]
		switch {
		case iok && jok && oi != oj:
			return oi < oj
		case iok != jok:
			return iok
		}
		return rows[i].ID < rows[j].ID
	})
	return Results{Rows: rows}
}

// target is one provider and the models of it a round asks.
type target struct {
	p      provider.Provider
	models []string
}

// targets is what a round asks: every provider that is on and has exposed
// models, less those of an asked list, and the models of each narrowed to
// models when that is not empty. A model id given as provider/id is asked of
// that provider alone.
func targets(only []string, models []string) []target {
	want := map[string]bool{}
	for _, id := range only {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	// provider/id pins the id to one provider
	pinned := map[string][]string{}
	for _, m := range models {
		if p, id, ok := strings.Cut(m, "/"); ok && p != "" && id != "" {
			pinned[p] = append(pinned[p], id)
		}
	}
	var out []target
	for _, p := range provider.All() {
		if p.Off {
			continue
		}
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		var ask []string
		if ids, ok := pinned[p.ID]; ok {
			ask = ids
		} else {
			for _, m := range p.Exposed() {
				if m.ID == "" {
					continue
				}
				if len(models) > 0 && !looseHas(models, m.ID) {
					continue
				}
				ask = append(ask, m.ID)
			}
		}
		if len(ask) == 0 {
			continue
		}
		out = append(out, target{p: p, models: ask})
	}
	return out
}

// looseHas is whether models names id, ignoring a provider/ a caller put
// before it (the pinning above reads those).
func looseHas(models []string, id string) bool {
	for _, m := range models {
		if m == id {
			return true
		}
		if _, bare, ok := strings.Cut(m, "/"); ok && bare == id {
			return true
		}
	}
	return false
}

// Exposed is every exposed model id of every provider that is on, with the
// providers serving it — what the page offers to pick a manual run from.
func Exposed() map[string][]string {
	out := map[string][]string{}
	for _, p := range provider.All() {
		if p.Off {
			continue
		}
		for _, m := range p.Exposed() {
			if m.ID == "" {
				continue
			}
			out[m.ID] = append(out[m.ID], p.ID)
		}
	}
	return out
}

// Start runs rounds on the plan's interval for as long as the process
// lasts: the first at once, so an install that has never looked shows what
// it finds rather than an empty page. It is safe to call more than once.
func Start() {
	startOnce.Do(func() { go loop() })
}

var startOnce sync.Once

// Stop ends the standing round, if one runs.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if stop != nil {
		stop()
		stop = nil
	}
}

// tick is how often loop looks at the plan: short enough that turning it
// on or changing its interval takes effect without a restart, long enough
// to cost nothing while it sits.
func loop() {
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	next := time.Now() // a process that starts looks at once
	for now := range tick.C {
		p, err := Load()
		if err != nil || !p.On {
			// turned off, or unreadable: nothing sent, and it stays that way
			// until the plan reads again
			next = now.Add(tickEvery)
			continue
		}
		if now.Before(next) {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		mu.Lock()
		stop = cancel
		mu.Unlock()
		Run(ctx, DoD{})
		cancel()
		mu.Lock()
		stop = nil
		mu.Unlock()
		next = now.Add(p.EveryDuration())
	}
}

// tickEvery is loop's look at the plan.
var tickEvery = 30 * time.Second

// setStep says what a running round is at, for the page.
func setStep(s string) {
	mu.Lock()
	current.Step = s
	mu.Unlock()
}

// Route is one routing group that carries a model id, with its members in
// the order a request falls back through them. A member of a group of
// groups is named by its own group first.
type Route struct {
	Group   string   `json:"group"`          // the group's id, without "group/"
	Name    string   `json:"name,omitempty"` // the group's name, as the window shows it
	Members []string `json:"members"`        // "provider/model", in order
}

// RoutesFor is every routing group that carries id, outermost groups first,
// each with its members in order. An id no group carries returns nothing:
// it is reachable only by its bare provider ids.
func RoutesFor(id string) []Route {
	find := provider.GroupFinder()
	var out []Route
	for _, g := range provider.Groups() {
		if g.Disabled || g.Hidden {
			continue
		}
		_, ms, ok := find(provider.GroupPrefix + g.ID)
		if !ok {
			continue
		}
		var members []string
		for _, m := range ms {
			if m.Model == id {
				members = append(members, m.Provider.ID+"/"+m.Model)
			}
		}
		if len(members) == 0 {
			continue
		}
		out = append(out, Route{Group: g.ID, Name: g.Name, Members: members})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}
