package gwplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/gwmw"
)

// home points the store at a folder of the test's.
func home(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mu.Lock()
	cache = nil
	mu.Unlock()
	ForgetAll()
	t.Cleanup(func() {
		mu.Lock()
		cache = nil
		mu.Unlock()
		ForgetAll()
	})
}

// echo is a plugin that answers /request with what it was sent, after
// edit (nil: leave it alone).
func echo(t *testing.T, edit func(map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/usage" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var in Request
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(in.Body, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if edit != nil {
			body = edit(body)
		}
		if body == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAPluginRewritesTheBody(t *testing.T) {
	home(t)
	s := echo(t, func(b map[string]any) map[string]any {
		b["system"] = "You are a red team lead."
		return b
	})
	if err := Put(Plugin{ID: "p", URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","stream":false,"system":"You are a coding agent."}`)
	out := Ask(context.Background(), Request{Protocol: "anthropic", Model: "m"}, body)
	if !strings.Contains(string(out), "red team lead") {
		t.Fatalf("the plugin's body wasn't used: %s", out)
	}
	// the model and the stream are the gateway's
	if !strings.Contains(string(out), `"model":"m"`) || !strings.Contains(string(out), `"stream":false`) {
		t.Fatalf("a field magpie keeps went: %s", out)
	}
}

func TestNoAnswerLeavesTheBodyAsItWas(t *testing.T) {
	home(t)
	// 204: nothing to say
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.Close)
	if err := Put(Plugin{ID: "p", URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, body)
	if string(out) != string(body) {
		t.Fatalf("an unchanged body was rebuilt: %s", out)
	}
}

func TestAPluginThatChangesTheModelIsRefused(t *testing.T) {
	home(t)
	s := echo(t, func(b map[string]any) map[string]any {
		b["model"] = "something-else"
		return b
	})
	if err := Put(Plugin{ID: "p", URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","messages":[]}`)
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, body)
	if strings.Contains(string(out), "something-else") {
		t.Fatalf("a plugin changed the model the caller's key was checked against: %s", out)
	}
	// and it counts as a failure
	if st := Statuses()[0]; st.Failures != 1 || st.Answered != 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestAPluginThatChangesTheStreamIsRefused(t *testing.T) {
	home(t)
	s := echo(t, func(b map[string]any) map[string]any {
		b["stream"] = true
		return b
	})
	if err := Put(Plugin{ID: "p", URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, []byte(`{"model":"m","stream":false}`))
	if strings.Contains(string(out), `"stream":true`) {
		t.Fatalf("a plugin turned a reply into a stream: %s", out)
	}
}

func TestADeadPluginLeavesTheRequestAlone(t *testing.T) {
	home(t)
	// nothing listens there
	if err := Put(Plugin{ID: "dead", URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m"}`)
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, body)
	if string(out) != string(body) {
		t.Fatalf("a dead plugin changed the body: %s", out)
	}
	// and rests after the threshold
	for range 3 {
		Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, body)
	}
	if st := Statuses()[0]; st.State != "resting" || st.Resting == "" {
		t.Fatalf("a failing plugin isn't resting: %+v", st)
	}
}

func TestTwoPluginsComposeInOrder(t *testing.T) {
	home(t)
	first := echo(t, func(b map[string]any) map[string]any {
		b["system"] = "one"
		return b
	})
	second := echo(t, func(b map[string]any) map[string]any {
		b["system"] = b["system"].(string) + "+two"
		return b
	})
	if err := Put(Plugin{ID: "a", URL: first.URL}); err != nil {
		t.Fatal(err)
	}
	if err := Put(Plugin{ID: "b", URL: second.URL}); err != nil {
		t.Fatal(err)
	}
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, []byte(`{"model":"m","system":"x"}`))
	if !strings.Contains(string(out), "one+two") {
		t.Fatalf("the two weren't composed: %s", out)
	}
}

func TestAPluginIsAskedOnlyForTheAgentsAndModelsItNames(t *testing.T) {
	home(t)
	asked := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.Close)
	if err := Put(Plugin{ID: "p", URL: s.URL, Agents: []string{"claude-code"}, Models: []string{"claude/*"}}); err != nil {
		t.Fatal(err)
	}
	Ask(context.Background(), Request{Protocol: "chat", Model: "gpt-5", Agent: "codex"}, []byte(`{"model":"gpt-5"}`))
	if asked != 0 {
		t.Fatal("a plugin was asked about an agent and a model it doesn't name")
	}
	Ask(context.Background(), Request{Protocol: "anthropic", Model: "claude/opus", Agent: "claude-code"}, []byte(`{"model":"claude/opus"}`))
	if asked != 1 {
		t.Fatalf("a plugin wasn't asked about what it names: %d", asked)
	}
}

func TestAPluginOffIsNotAsked(t *testing.T) {
	home(t)
	s := echo(t, func(b map[string]any) map[string]any {
		b["system"] = "changed"
		return b
	})
	if err := Put(Plugin{ID: "p", URL: s.URL, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, []byte(`{"model":"m"}`))
	if strings.Contains(string(out), "changed") {
		t.Fatal("a plugin that is off was asked")
	}
}

func TestARejectEndsTheRequest(t *testing.T) {
	home(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"reject": gwmw.Reject{Status: 403, Message: "not today"}})
	}))
	t.Cleanup(s.Close)
	if err := Put(Plugin{ID: "p", URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	// a reject is the plugin's say, and leaves the request as it was: the
	// gateway's own refusals are its access control's
	out := Ask(context.Background(), Request{Protocol: "chat", Model: "m"}, []byte(`{"model":"m"}`))
	if string(out) != `{"model":"m"}` {
		t.Fatalf("a reject changed the body: %s", out)
	}
}

func TestTheStoreKeepsAndChecks(t *testing.T) {
	home(t)
	if err := Put(Plugin{ID: "a b", URL: "http://x:1"}); err == nil {
		t.Fatal("an id with a space was taken")
	}
	if err := Put(Plugin{ID: "a"}); err == nil {
		t.Fatal("a plugin with no address was taken")
	}
	if err := Put(Plugin{ID: "a", URL: "http://x:1", Timeout: "soon"}); err == nil {
		t.Fatal("a timeout that isn't a length of time was taken")
	}
	if err := Put(Plugin{ID: "a", URL: "host:8080"}); err != nil {
		t.Fatalf("a bare host:port: %v", err)
	}
	if got, ok := Get("a"); !ok || got.Base() != "http://host:8080" {
		t.Fatalf("get %+v %v", got, ok)
	}
	// saved to disk, and read back by a fresh cache
	if err := Save(Store{Plugins: []Plugin{{ID: "b", URL: "http://y:2"}}}); err != nil {
		t.Fatal(err)
	}
	if s := Read(); len(s.Plugins) != 1 || s.Plugins[0].ID != "b" {
		t.Fatalf("read %+v", s)
	}
	if err := Remove("b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get("b"); ok {
		t.Fatal("removed")
	}
}

func TestDefaultsAreTheDocs(t *testing.T) {
	p := Plugin{}
	if p.TimeoutOr().Milliseconds() != 1500 || p.Failed() != 3 || p.Resting().Seconds() != 30 {
		t.Fatalf("%s %d %s", p.TimeoutOr(), p.Failed(), p.Resting())
	}
}
