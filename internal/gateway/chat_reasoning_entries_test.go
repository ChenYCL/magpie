package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// capture relays a Chat reply and keeps the body it was sent, so a test can
// read what the upstream got.
type capture struct {
	mu   sync.Mutex
	sent []byte
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.sent = b
	c.mu.Unlock()
	w.Header().Set("content-type", "application/json")
	w.Write([]byte(`{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (c *capture) body() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent
}

// A client that sends an earlier turn's thinking as OpenRouter's entries
// list under reasoning, which a relay in front of a server unmarshalling
// reasoning into a string answers with a 500 of its own ("cannot unmarshal
// array into Go struct field …reasoning of type string", 色哥): the text
// reaches the upstream as the reasoning_content every Chat server reads,
// and the list is left out.
func TestChatRelayTurnsReasoningEntriesIntoContent(t *testing.T) {
	fresh(t)
	c := &capture{}
	up := httptest.NewServer(c)
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "k", Chat: up.URL + "/v1", Models: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
		`{"model":"a/m","messages":[`+
			`{"role":"user","content":"hi"},`+
			`{"role":"assistant","content":"yo","reasoning":[{"type":"reasoning.text","text":"thought one"}]},`+
			`{"role":"user","content":"again"}]}`)))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var q struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(c.body(), &q); err != nil {
		t.Fatalf("%v in %s", err, c.body())
	}
	if len(q.Messages) != 3 {
		t.Fatalf("messages %s", c.body())
	}
	if _, ok := q.Messages[1]["reasoning"]; ok {
		t.Fatalf("the entries list went upstream: %s", c.body())
	}
	var think string
	if err := json.Unmarshal(q.Messages[1]["reasoning_content"], &think); err != nil || think != "thought one" {
		t.Fatalf("reasoning_content %q %v in %s", think, err, c.body())
	}
}

// A client whose thinking is one string reaches the upstream as it came.
func TestChatRelayKeepsStringReasoningAsSent(t *testing.T) {
	fresh(t)
	c := &capture{}
	up := httptest.NewServer(c)
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "k", Chat: up.URL + "/v1", Models: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
		`{"model":"a/m","messages":[{"role":"user","content":"hi"},`+
			`{"role":"assistant","content":"yo","reasoning":"thought one"},{"role":"user","content":"again"}]}`)))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(c.body()), `"reasoning":"thought one"`) {
		t.Fatalf("a string reasoning was rewritten: %s", c.body())
	}
}

// reasoningText reads the entries list a thinking field holds, under any of
// its spellings, so a translated request keeps the thought.
func TestReasoningTextReadsTheEntriesList(t *testing.T) {
	m := func(k, v string) map[string]json.RawMessage {
		return map[string]json.RawMessage{k: json.RawMessage(v)}
	}
	if got := reasoningText(m("reasoning", `[{"type":"reasoning.text","text":"a"},{"type":"reasoning.text","text":"b"}]`)); got != "ab" {
		t.Fatalf("reasoning entries %q", got)
	}
	if got := reasoningText(m("reasoning_content", `[{"summary":"sum"}]`)); got != "sum" {
		t.Fatalf("reasoning_content entries %q", got)
	}
	if got := reasoningText(m("reasoning", `"plain"`)); got != "plain" {
		t.Fatalf("a string %q", got)
	}
}
