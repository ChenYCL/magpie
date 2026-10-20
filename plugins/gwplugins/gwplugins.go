// Package gwpluginplugin is the gateway's plugin layer in magpie's own
// request path: every agent's request goes through the plugins the user
// installed as gateway plugins (internal/gwplugin), and every usage row
// reaches the ones that asked for the ledger.
//
// The plugins are programs of their own — an HTTP endpoint each, in any
// language — and this is what puts them where they can do their work: after
// the caller's key is checked and the model resolved, so a plugin is told the
// agent, the model and the provider (which an in-front proxy cannot see), and
// before the vendor is sent anything, so its answer is what the vendor gets.
//
// Off, requests go exactly as they came, and no row is forwarded: a magpie
// with this plugin off is a magpie with no gateway plugins, whatever
// plugins.d/gateway-plugins.json holds.
package gwpluginplugin

import (
	"context"
	"net/http"

	"github.com/yetone/magpie/internal/gwplugin"
	"github.com/yetone/magpie/internal/gwmw"
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/usage"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "gwplugins", API: "^1", Requires: []string{"gateway"}, Essential: false,
		Name: "Gateway plugins", Sub: "Programs of your own on every request: an HTTP endpoint each, in any language, that may rewrite the body the vendor gets",
		One: "1 gateway plugin installed", Many: "{n} gateway plugins installed",
		InUse: func() (int, error) { return gwplugin.On(), nil },
		Apply: func(c *pluginrt.Ctx) error {
			c.OnDispose(gwmw.Use(Agent{}))
			if anyUsage() {
				c.OnDispose(usage.Observe(Ledger{}))
			}
			return nil
		},
	})
}

// anyUsage says some plugin asked for the ledger.
func anyUsage() bool {
	for _, p := range gwplugin.All() {
		if p.Usage && !p.Disabled {
			return true
		}
	}
	return false
}

// Agent is the middleware: the body an agent sent, as the gateway plugin
// chain leaves it.
type Agent struct{}

type run struct{ info gwmw.Info }

// Begin is asked as a request starts.
func (Agent) Begin(info gwmw.Info) gwmw.Run { return &run{info: info} }

// Request is the body after every plugin that takes it.
func (r *run) Request(body []byte) ([]byte, *gwmw.Reject) {
	in := gwplugin.Request{
		Protocol: r.info.Protocol,
		Model:    r.info.Model,
		Agent:    r.info.Agent,
		Path:     r.info.Path,
		Stream:   r.info.Stream,
	}
	// the agent middleware is told no session and no provider: both are
	// the gateway's own, and a plugin that needs them reads them from the
	// usage rows it asked for
	return gwplugin.Ask(context.Background(), in, body), nil
}

// Wrap is the reply as it is: a gateway plugin edits the request alone (a
// plugin that wants to see replies asks for the ledger).
func (r *run) Wrap(w http.ResponseWriter) (http.ResponseWriter, func()) { return w, func() {} }

// End is the request's end.
func (r *run) End() {}

// Ledger is the usage observer: each row goes to the plugins that asked for
// the ledger, in the background.
type Ledger struct{}

// Serve starts observing.
func (Ledger) Serve() func() {
	gwplugin.StartLedger()
	return gwplugin.StopLedger
}

// Begin watches no request: this observer takes the rows alone.
func (Ledger) Begin(usage.Request) usage.Watch { return nil }

// Ledger takes one row.
func (Ledger) Ledger(row usage.Record) { gwplugin.Send(row) }

// Bodies says this observer wants none of the bodies: a plugin that asked
// for the ledger is sent the row, not the conversation.
func (Ledger) Bodies() usage.Bodies { return usage.NoBodies }
