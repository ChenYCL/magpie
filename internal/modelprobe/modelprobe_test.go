package modelprobe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// probeHome points magpie's files at a temp dir, as the other packages' tests do.
func probeHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Cleanup(func() {
		mu.Lock()
		current = Results{}
		asking = false
		mu.Unlock()
	})
}

// TestPlanSettlesItsOwnFields: an interval too short is no interval at all,
// the default standing in, so a plan cannot ask for a round a second.
func TestPlanSettlesItsOwnFields(t *testing.T) {
	p := Plan{On: true, Every: "5s"}
	p.Normalize()
	if p.Every != "" {
		t.Fatalf("a 5s interval stood: %+v", p)
	}
	if got := p.EveryDuration(); got != DefaultEvery {
		t.Fatalf("default interval %v, want %v", got, DefaultEvery)
	}
	p = Plan{Every: "45m"}
	p.Normalize()
	if got := p.EveryDuration(); got != 45*time.Minute {
		t.Fatalf("45m became %v", got)
	}
}

// TestPlanKeepsIdsOnceInOrder: the ids a plan names keep their order, and one
// named twice is asked once.
func TestPlanKeepsIdsOnceInOrder(t *testing.T) {
	p := Plan{Models: []string{" b ", "", "a", "b"}}
	p.Normalize()
	if len(p.Models) != 2 || p.Models[0] != "b" || p.Models[1] != "a" {
		t.Fatalf("ids: %v", p.Models)
	}
}

// TestLoadOffWhenNoFile: an install that never opened the page has no
// file, and that is a plan that sends nothing — not an error.
func TestLoadOffWhenNoFile(t *testing.T) {
	probeHome(t)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.On {
		t.Fatalf("no file, and it is on: %+v", p)
	}
	if got := p.EveryDuration(); got != DefaultEvery {
		t.Fatalf("interval %v", got)
	}
}

// TestSaveThenLoadRoundTrips: what the page saves is what it reads back.
func TestSaveThenLoadRoundTrips(t *testing.T) {
	probeHome(t)
	want := Plan{On: true, Every: "1h", Models: []string{"a/b"}}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.On || got.Every != "1h" || len(got.Models) != 1 || got.Models[0] != "a/b" {
		t.Fatalf("read back %+v", got)
	}
}

// TestMergeOneLinePerID: the same id at three providers is one row, its
// providers behind it, and the row says how many answered.
func TestMergeOneLinePerID(t *testing.T) {
	found := map[string][]Status{
		"gpt-x": {
			{Provider: "b", Model: "gpt-x", OK: false, Error: "401", At: 10},
			{Provider: "a", Model: "gpt-x", OK: true, At: 20},
			{Provider: "c", Model: "gpt-x", OK: true, At: 5},
		},
		"other": {{Provider: "a", Model: "other", OK: true, At: 1}},
	}
	out := merge(found)
	if len(out.Rows) != 2 {
		t.Fatalf("rows: %+v", out.Rows)
	}
	var row *Row
	for i := range out.Rows {
		if out.Rows[i].ID == "gpt-x" {
			row = &out.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("gpt-x is missing")
	}
	if row.N != 3 || row.OK != 2 {
		t.Fatalf("gpt-x %d of %d", row.OK, row.N)
	}
	if row.At != 20 {
		t.Fatalf("newest answer %d, want 20", row.At)
	}
	for i, want := range []string{"a", "b", "c"} {
		if row.Providers[i].Provider != want {
			t.Fatalf("providers %v", row.Providers)
		}
	}
}

// TestPutStatusKeepsTheOthers: asking one provider again does not forget what
// the others answered.
func TestPutStatusKeepsTheOthers(t *testing.T) {
	list := []Status{{Provider: "a", OK: true}, {Provider: "b", OK: false}}
	list = putStatus(list, Status{Provider: "b", OK: true})
	if len(list) != 2 || !list[0].OK || !list[1].OK {
		t.Fatalf("after: %+v", list)
	}
}

// TestTargetsSkipOffAndPinByProvider: a provider that is off sends nothing,
// and provider/id pins an id to that one provider.
func TestTargetsSkipOffAndPinByProvider(t *testing.T) {
	probeHome(t)
	save := func(id string, off bool, models ...string) {
		t.Helper()
		if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "k", Chat: "http://127.0.0.1:1/v1", Off: off, Models: models}); err != nil {
			t.Fatal(err)
		}
	}
	save("on", false, "m1", "m2")
	save("gone", true, "m1")

	all := targets(nil, nil)
	for _, tt := range all {
		if tt.p.ID == "gone" {
			t.Fatalf("a provider that is off was asked: %+v", tt)
		}
	}
	pinned := targets(nil, []string{"on/m2"})
	if len(pinned) != 1 || pinned[0].p.ID != "on" || len(pinned[0].models) != 1 || pinned[0].models[0] != "m2" {
		t.Fatalf("pinned: %+v", pinned)
	}
	if got := targets([]string{"gone"}, nil); len(got) != 0 {
		t.Fatalf("a provider named but off was asked: %+v", got)
	}
}

// TestRunRefusesASecondRound: the standing round and the page's cannot ask a
// vendor the same models twice over.
func TestRunRefusesASecondRound(t *testing.T) {
	probeHome(t)
	mu.Lock()
	asking = true
	mu.Unlock()
	if _, err := Run(context.Background(), DoD{}); err != ErrRunning {
		t.Fatalf("a second round: %v", err)
	}
}

// TestResultsSurviveARestart: the page shows the last round's findings
// before this process's first round, rather than an empty list.
func TestResultsSurviveARestart(t *testing.T) {
	probeHome(t)
	if err := os.MkdirAll(filepath.Dir(ResultsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Results{Rows: []Row{{ID: "kept", N: 1, OK: 1}}, At: 99})
	if err := os.WriteFile(ResultsPath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	current = Results{}
	mu.Unlock()
	out := Last()
	if len(out.Rows) != 1 || out.Rows[0].ID != "kept" || out.At != 99 {
		t.Fatalf("kept: %+v", out)
	}
}

// TestProbeWritesNothingWhenItLacksAModelList: a provider whose list has no
// models is not asked at all — no request is sent for a model it doesn't have.
func TestTargetsOfAProviderWithNoModels(t *testing.T) {
	probeHome(t)
	if err := provider.Save(provider.Provider{ID: "empty", Name: "empty", Key: "k", Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range targets(nil, nil) {
		if tt.p.ID == "empty" {
			t.Fatalf("a provider with no models was asked: %+v", tt)
		}
	}
}

// TestExposedMergesProvidersById keeps the map a manual run picks from: one
// id, every provider serving it.
func TestExposedMergesProvidersById(t *testing.T) {
	probeHome(t)
	for _, id := range []string{"a", "b"} {
		if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"shared", id + "-own"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.Save(provider.Provider{ID: "off", Name: "off", Key: "k", Chat: "http://127.0.0.1:1/v1", Off: true, Models: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}
	got := Exposed()
	if len(got["shared"]) != 2 {
		t.Fatalf("shared: %v", got["shared"])
	}
	if _, ok := got["a-own"]; !ok {
		t.Fatal("a-own is missing")
	}
}
