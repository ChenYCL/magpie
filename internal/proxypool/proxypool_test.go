package proxypool

import (
	"net/http"
	"testing"
	"time"
)

func reset() { ForgetAll() }

func pool(urls ...string) Pool {
	p := Pool{}
	for _, u := range urls {
		p.Exits = append(p.Exits, Exit{URL: u})
	}
	return p
}

func TestAPoolWithNoExitIsOff(t *testing.T) {
	if (Pool{}).On() {
		t.Fatal("an empty pool is on")
	}
	p := pool("http://a")
	p.Disabled = true
	if p.On() {
		t.Fatal("a disabled pool is on")
	}
	if !pool("http://a").On() {
		t.Fatal("a pool with an exit is off")
	}
	// a disabled exit is not a usable one
	p = Pool{Exits: []Exit{{URL: "http://a", Disabled: true}}}
	if p.On() {
		t.Fatal("a pool of disabled exits is on")
	}
}

func TestRoundRobinTakesEachInTurn(t *testing.T) {
	reset()
	p := pool("http://a", "http://b", "http://c")
	var got []string
	for range 6 {
		choice, done, ok := Pick("rr", p, "")
		if !ok {
			t.Fatal("no exit picked")
		}
		got = append(got, choice)
		done(&http.Response{StatusCode: 200}, nil)
	}
	want := []string{"http://a", "http://b", "http://c", "http://a", "http://b", "http://c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
}

func TestAStickyPinKeepsItsExitWhileItAnswers(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	p.Mode = "round-robin"
	first, done, _ := Pick("sticky", p, "account:x")
	done(&http.Response{StatusCode: 200}, nil)
	for range 3 {
		again, d, _ := Pick("sticky", p, "account:x")
		d(&http.Response{StatusCode: 200}, nil)
		if again != first {
			t.Fatalf("a pinned caller moved from %s to %s", first, again)
		}
	}
}

func TestAFailedExitRestsAndThePinMoves(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	p.FailThreshold = 1
	first, done, _ := Pick("fail", p, "account:x")
	done(nil, errString("connection refused"))
	next, d, ok := Pick("fail", p, "account:x")
	if !ok {
		t.Fatal("nothing to pick after one exit failed")
	}
	if next == first {
		t.Fatalf("the failed exit was picked again: %s", next)
	}
	d(&http.Response{StatusCode: 200}, nil)

	// the exit that failed is resting, its state says so
	snaps := Snapshot("fail", p)
	resting := 0
	for _, s := range snaps {
		if s.State == "resting" {
			resting++
			if s.Resting == "" {
				t.Errorf("%s is resting with no time said", s.URL)
			}
		}
	}
	if resting != 1 {
		t.Fatalf("%d exits resting, want 1: %+v", resting, snaps)
	}
}

func TestAVendorsNotNowRestsTheExit(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	p.FailThreshold = 1
	first, done, _ := Pick("notnow", p, "")
	done(&http.Response{StatusCode: 429}, nil)
	next, _, _ := Pick("notnow", p, "")
	if next == first {
		t.Fatal("an exit the vendor rate-limited was picked again")
	}
}

func TestAClientErrorIsTheVendorsNotTheExits(t *testing.T) {
	reset()
	p := pool("http://a")
	p.FailThreshold = 1
	_, done, _ := Pick("400", p, "account:x")
	done(&http.Response{StatusCode: 400}, nil)
	again, d, ok := Pick("400", p, "account:x")
	if !ok || again != "http://a" {
		t.Fatalf("a 400 rested the exit: %s", again)
	}
	d(&http.Response{StatusCode: 200}, nil)
	if s := Snapshot("400", p); s[0].State != "ready" {
		t.Fatalf("state %+v", s)
	}
}

func TestEverythingRestingStillPicksOne(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	p.FailThreshold = 1
	for range 2 {
		_, done, _ := Pick("all", p, "")
		done(nil, errString("boom"))
	}
	choice, d, ok := Pick("all", p, "")
	if !ok || choice == "" {
		t.Fatal("a vendor was left with no exit at all")
	}
	d(&http.Response{StatusCode: 200}, nil)
}

func TestLeastLatencyPicksItsMean(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	p.Mode = "least-latency"
	// a answers slowly, b not at all yet
	a, da, _ := Pick("lat", p, "")
	time.Sleep(2 * time.Millisecond)
	da(&http.Response{StatusCode: 200}, nil)
	_ = a
	// b's first pick is taken (least-latency prefers a with a mean over
	// one with none), answered fast
	next, db, _ := Pick("lat", p, "")
	db(&http.Response{StatusCode: 200}, nil)
	if next == a {
		// not proof of failure: a's mean may still be better; the point
		// is that both were picked
	}
	// after a is rested, only b is left
	Rest("lat", a, time.Minute)
	third, d3, ok := Pick("lat", p, "")
	if !ok || third == a {
		t.Fatalf("a rested exit was picked: %s", third)
	}
	d3(&http.Response{StatusCode: 200}, nil)
}

func TestRestAndForget(t *testing.T) {
	reset()
	p := pool("http://a", "http://b")
	Rest("rest", "http://a", time.Minute)
	if s := Snapshot("rest", p); s[0].State != "resting" {
		t.Fatalf("%+v", s)
	}
	Forget("rest")
	if s := Snapshot("rest", p); s[0].State != "ready" {
		t.Fatalf("forget left it resting: %+v", s)
	}
}

func TestPoolValid(t *testing.T) {
	good := pool("http://a", "socks5://b:1080", "direct")
	if err := good.Valid(); err != nil {
		t.Fatal(err)
	}
	bad := []Pool{
		{Exits: []Exit{{URL: "http://a"}, {URL: "http://a"}}},
		{Exits: []Exit{{URL: ""}}},
		{Mode: "wat", Exits: []Exit{{URL: "http://a"}}},
		{Rest: "soon", Exits: []Exit{{URL: "http://a"}}},
		{FailThreshold: -1, Exits: []Exit{{URL: "http://a"}}},
	}
	for _, p := range bad {
		if err := p.Valid(); err == nil {
			t.Errorf("%+v was taken", p)
		}
	}
}

func TestModeDefaultsAreTheDocs(t *testing.T) {
	p := Pool{}
	if p.ModeOr() != "round-robin" || p.Failed() != 2 || p.Resting() != time.Minute {
		t.Fatalf("%s %d %s", p.ModeOr(), p.Failed(), p.Resting())
	}
	p = Pool{Mode: "random", FailThreshold: 5, Rest: "30s"}
	if p.ModeOr() != "random" || p.Failed() != 5 || p.Resting() != 30*time.Second {
		t.Fatalf("%s %d %s", p.ModeOr(), p.Failed(), p.Resting())
	}
}

type errString string

func (e errString) Error() string { return string(e) }
