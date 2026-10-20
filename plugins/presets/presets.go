// Package presetsplugin is agent presets in the gateway: every request an
// agent sends goes through the preset it gets (internal/presets), and what
// each run did is kept for the window.
//
// It is a plugin package rather than part of the presets' store, so that
// the store — which the window and the CLI read and write — carries none
// of the request path's machinery, and a page's save links no middleware.
package presetsplugin

import (
	"net/http"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/gwmw"
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/presets"
)

// Applied is one request's preset run, as the window shows it.
type Applied struct {
	At       int64         `json:"at"`
	Agent    string        `json:"agent"`
	Preset   string        `json:"preset"`
	Protocol string        `json:"protocol"`
	Model    string        `json:"model"`
	Stats    presets.Stats `json:"stats"`
}

var (
	mu   sync.Mutex
	ring []Applied
)

// ringKeep is how many runs the window sees, newest last.
const ringKeep = 50

func note(a Applied) {
	mu.Lock()
	defer mu.Unlock()
	a.At = time.Now().UnixMilli()
	ring = append(ring, a)
	if len(ring) > ringKeep {
		ring = ring[len(ring)-ringKeep:]
	}
}

// Recent is the last runs, newest first.
func Recent() []Applied {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Applied, 0, len(ring))
	for i := len(ring) - 1; i >= 0; i-- {
		out = append(out, ring[i])
	}
	return out
}

// Forget clears the runs this process kept. Tests.
func Forget() {
	mu.Lock()
	ring = nil
	mu.Unlock()
}

// Agent is the middleware: the body an agent sent, edited with its preset.
type Agent struct{}

type run struct{ info gwmw.Info }

// Begin is asked as a request starts.
func (Agent) Begin(info gwmw.Info) gwmw.Run { return &run{info: info} }

// Request is the body after the preset; as it came when the agent has one
// that changes nothing, and as it came when it has none.
func (r *run) Request(body []byte) ([]byte, *gwmw.Reject) {
	p, ok := presets.For(r.info.Agent)
	if !ok {
		return body, nil
	}
	out, st := presets.Apply(p, r.info.Protocol, body)
	note(Applied{Agent: r.info.Agent, Preset: p.ID, Protocol: r.info.Protocol, Model: r.info.Model, Stats: st})
	return out, nil
}

// Wrap is the reply as it is: a preset edits the request alone.
func (r *run) Wrap(w http.ResponseWriter) (http.ResponseWriter, func()) { return w, func() {} }

// End is the request's end.
func (r *run) End() {}

// InUse is 1 while any agent has a preset that does something: a preset
// with no patch, or one whose patches are all off, is not in use.
func InUse() (int, error) {
	list := presets.All()
	n := 0
	for _, p := range list {
		if p.Disabled {
			continue
		}
		for _, pa := range p.Patches {
			if !pa.Disabled {
				n++
				break
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	return 1, nil
}

func init() {
	// the window's page reads what the last requests did through core's
	// hook: a page route may link no plugin package
	gateway.PresetsRecent = func() any { return Recent() }
	pluginrt.Register(pluginrt.Manifest{
		ID: "presets", API: "^1", Requires: []string{"gateway"}, Essential: false,
		Name: "Agent presets", Sub: "Which prompt, context files and tools each agent's requests get, and the patch that switches between them",
		One: "Presets are on", Many: "Presets are on",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "presets"}},
		Client: []string{"presets.css", "client.js"},
		InUse: InUse,
		Apply: func(c *pluginrt.Ctx) error {
			c.OnDispose(gwmw.Use(Agent{}))
			return nil
		},
	})
}
