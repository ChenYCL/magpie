package provider

import (
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/proxypool"
)

// poolPinHeader pins a request to one of a provider's pool exits, by the
// exit's label or its address: an agent or a probe asking for one exit of
// a pool, as AccountHeader asks for one account. It goes no further than
// magpie.
const poolPinHeader = "X-Magpie-Proxy"

// poolPick asks the provider's pool which exit this request takes: the
// choice netproxy.With takes, a done to report with what came back, and ok
// false when the provider has no pool to pick from (the request then goes
// as it did before).
//
// The caller a pool is sticky by is, in this order: the request's own pin
// header, the account the request is for, the key it is made with, and the
// conversation it belongs to — so a vendor's session stays on one exit
// while that exit answers.
func poolPick(p Provider, req *http.Request) (choice string, done func(*http.Response, error), ok bool) {
	if !p.ProxyPool.On() {
		return "", nil, false
	}
	pin := poolPin(p, req)
	if v := strings.TrimSpace(req.Header.Get(poolPinHeader)); v != "" {
		req.Header.Del(poolPinHeader)
		for _, e := range p.ProxyPool.Exits {
			if strings.EqualFold(e.Label, v) || e.URL == v {
				pin = "pinned:" + e.URL
				break
			}
		}
	}
	return proxypool.Pick(p.ID, p.ProxyPool, pin)
}

// poolPin is the caller the pool's stickiness is kept by.
func poolPin(p Provider, req *http.Request) string {
	if p.Account != nil && p.Account.User != "" {
		return "account:" + strings.ToLower(p.Account.User)
	}
	if v := req.Header.Get(ConversationHeader); v != "" {
		return "session:" + v
	}
	if p.Key != "" {
		return "key:" + keyID(p.Key)
	}
	return ""
}
