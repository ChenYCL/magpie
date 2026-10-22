package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The plan: what a box may be, and that an install starts with none.
func TestDefaultPlanIsOff(t *testing.T) {
	p := Default()
	if p.On {
		t.Fatal("a fresh install has the box off: the broker is the user's to start")
	}
	if p.Network != "none" {
		t.Fatalf("a box has no network unless asked: %q", p.Network)
	}
}

func TestPlanNormalizeHoldsTheEdges(t *testing.T) {
	p := Plan{Network: "everywhere", Limits: Limits{MaxBoxes: 99}}
	p.Normalize()
	if p.Network != "none" {
		t.Fatalf("unknown network: %q", p.Network)
	}
	if p.Limits.MaxBoxes != 4 {
		t.Fatalf("boxes %d, want the ceiling 4", p.Limits.MaxBoxes)
	}
	p = Plan{Limits: Limits{MaxBoxes: -3}}
	p.Normalize()
	if p.Limits.MaxBoxes != 1 {
		t.Fatalf("boxes %d, want the floor 1", p.Limits.MaxBoxes)
	}
}

func TestPlanRefusesABrokerOffThisComputer(t *testing.T) {
	err := Save(Plan{On: true, Broker: "http://10.0.0.9:8794", Image: "debian:12-slim"})
	if err == nil {
		t.Fatal("a broker on the network was accepted")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("%v", err)
	}
}

func TestPlanRefusesAnImageThatIsAClause(t *testing.T) {
	p := Plan{Broker: DefaultBroker}
	p.Normalize()
	if p.Image != DefaultImage {
		t.Fatalf("an empty image is the default, got %q", p.Image)
	}
	for _, bad := range []string{"debian --privileged", "x;id", "$(id)"} {
		if err := Save(Plan{Broker: DefaultBroker, Image: bad}); err == nil {
			t.Fatalf("image %q was accepted", bad)
		}
	}
}

// The client: off is refused as such, not run here.
func TestRunRefusesWhenOff(t *testing.T) {
	_, err := NewClient().Run(context.Background(), Default(), Exec{Command: "id"})
	if !errors.Is(err, ErrOff) {
		t.Fatalf("%v, want ErrOff", err)
	}
}

// A broker that isn't there is ErrDown, never a command run elsewhere.
func TestRunSaysDownWhenNoBrokerAnswers(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens now
	p := Default()
	p.On = true
	SetBroker(&p, "http://"+addr)
	_, err = NewClient().Run(context.Background(), p, Exec{Command: "id"})
	if !errors.Is(err, ErrDown) {
		t.Fatalf("%v, want ErrDown", err)
	}
}

// The client hands the broker the command as it is and reads the answer.
func TestRunReadsTheBrokersAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/exec" || r.Method != http.MethodPost {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		var in Exec
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Command != "uname -a" {
			t.Fatalf("command %q", in.Command)
		}
		_ = json.NewEncoder(rw).Encode(Result{Stdout: "Linux\n", Exit: 0})
	}))
	defer srv.Close()
	p := Default()
	p.On = true
	p.Broker = srv.URL
	res, err := NewClient().Run(context.Background(), p, Exec{Command: "uname -a"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "Linux\n" || res.Exit != 0 {
		t.Fatalf("%+v", res)
	}
}

// A non-zero exit is the command's answer, not a magpie failure.
func TestClientKeepsANonZeroExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(Result{Stderr: "no such file\n", Exit: 127})
	}))
	defer srv.Close()
	p := Default()
	p.On = true
	p.Broker = srv.URL
	res, err := NewClient().Run(context.Background(), p, Exec{Command: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 127 {
		t.Fatalf("exit %d", res.Exit)
	}
}

// fakeEngine is a docker CLI that records its arguments and prints what a
// test says.
type fakeEngine struct {
	dir   string
	calls *os.File
}

func newFakeEngine(t *testing.T, stdout, stderr string, exit int) *fakeEngine {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + log + "\n" +
		"printf '%s' '" + stdout + "'\n" +
		"printf '%s' '" + stderr + "' >&2\n" +
		"exit " + itoa(exit) + "\n"
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeEngine{dir: dir, calls: f}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

func (f *fakeEngine) args(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The broker's box: no network unless asked, no capability, read-only root,
// a writable /work, and the plan's limits — whatever the body says.
func TestBrokerStartsALockedDownBox(t *testing.T) {
	eng := newFakeEngine(t, "hello\n", "", 0)
	b := &Broker{Plan: Default(), Engine: filepath.Join(eng.dir, "docker")}
	b.Plan.On = true
	srv := httptest.NewServer(b.handler())
	defer srv.Close()

	body := `{"command":"id"}`
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s", resp.Status)
	}
	var res Result
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if res.Stdout != "hello\n" || res.Exit != 0 {
		t.Fatalf("%+v", res)
	}
	args := eng.args(t)
	for _, want := range []string{
		"--rm", "--network none", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--read-only", "--memory 1024m", "--cpus 2", "--pids-limit 512",
		"magpie-sandbox=1", "/work", "id",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("no %q in: %s", want, args)
		}
	}
	if strings.Contains(args, "privileged") || strings.Contains(args, "docker.sock") {
		t.Fatalf("a box was asked for the host: %s", args)
	}
}

// A box is a box: the body cannot pick the network or mount the host.
func TestBrokerIgnoresHostInTheBody(t *testing.T) {
	eng := newFakeEngine(t, "", "", 0)
	b := &Broker{Plan: Default(), Engine: filepath.Join(eng.dir, "docker")}
	b.Plan.On = true
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	// fields a caller might invent: none of them is read
	body := `{"command":"id","network":"host","privileged":true,"mounts":["/:/host"]}`
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	args := eng.args(t)
	if strings.Contains(args, "--network host") || strings.Contains(args, "privileged") || strings.Contains(args, "/:/host") {
		t.Fatalf("the body reached the engine: %s", args)
	}
}

// An egress plan lets the box out, and still mounts nothing.
func TestBrokerEgressIsTheDefaultBridge(t *testing.T) {
	eng := newFakeEngine(t, "", "", 0)
	b := &Broker{Plan: Default(), Engine: filepath.Join(eng.dir, "docker")}
	b.Plan.On = true
	b.Plan.Network = "egress"
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(`{"command":"curl -s ifconfig.me"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	args := eng.args(t)
	if strings.Contains(args, "--network none") || strings.Contains(args, "--network host") {
		t.Fatalf("egress was not the default bridge: %s", args)
	}
}

// A command that runs long is ended with 124, and says so.
func TestBrokerEndsALongCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Broker{Plan: Default(), Engine: bin}
	b.Plan.On = true
	b.Plan.Limits.Seconds = 1
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	start := time.Now()
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(`{"command":"sleep 30"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res Result
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if res.Exit != 124 {
		t.Fatalf("exit %d, want 124: %+v", res.Exit, res)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("took %v", took)
	}
	if !strings.Contains(res.Stderr, "ended") {
		t.Fatalf("stderr %q", res.Stderr)
	}
}

// The cap holds: a third command while two run is refused, not queued
// forever.
func TestBrokerHoldsTheBoxCeiling(t *testing.T) {
	eng := newFakeEngine(t, "", "", 0)
	b := &Broker{Plan: Default(), Engine: filepath.Join(eng.dir, "docker")}
	b.Plan.On = true
	b.Plan.Limits.MaxBoxes = 1
	// hold one slot the way a running command does
	b.mu.Lock()
	b.busy = 1
	b.mu.Unlock()
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(`{"command":"id"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("%s", resp.Status)
	}
	var e map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e["error"], "in use") {
		t.Fatalf("%v", e)
	}
}

// Output is capped: a command that prints without end doesn't fill magpie.
func TestBrokerCapsOutput(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nhead -c 5000000 /dev/zero | tr '\\0' 'x'\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Broker{Plan: Default(), Engine: bin}
	b.Plan.On = true
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/exec", "application/json", strings.NewReader(`{"command":"yes"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res Result
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if len(res.Stdout) > outputCap {
		t.Fatalf("stdout %d bytes", len(res.Stdout))
	}
}

// Restore takes every labelled box away.
func TestBrokerResetRemovesLabelledBoxes(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + log + "\n" +
		"case \"$1\" in ps) echo abc123; echo def456;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Broker{Plan: Default(), Engine: bin}
	srv := httptest.NewServer(b.handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["removed"] != float64(2) {
		t.Fatalf("%v", out)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "rm -f abc123") || !strings.Contains(string(calls), "rm -f def456") {
		t.Fatalf("%s", calls)
	}
}

// The engine's binary is only ever run with the arguments the broker built:
// no argument comes from the request.
var _ = exec.Command
