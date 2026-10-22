package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// BrokerState asks a broker how many boxes run now, and its ceiling.
func BrokerState(ctx context.Context, p Plan) (running, max int, err error) {
	var out struct {
		Running int `json:"running"`
		Max     int `json:"max"`
	}
	if err = getJSON(ctx, p, "/boxes", &out); err != nil {
		return 0, 0, err
	}
	return out.Running, out.Max, nil
}

// BrokerReset takes every box away, answering how many went.
func BrokerReset(ctx context.Context, p Plan) (int, error) {
	var out struct {
		Removed int `json:"removed"`
	}
	if err := postJSON(ctx, p, "/reset", `{}`, &out); err != nil {
		return 0, err
	}
	return out.Removed, nil
}

func getJSON(ctx context.Context, p Plan, path string, into any) error {
	p.Normalize()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Broker, "/")+path, nil)
	if err != nil {
		return err
	}
	return doBroker(req, into)
}

func postJSON(ctx context.Context, p Plan, path, body string, into any) error {
	p.Normalize()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.Broker, "/")+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doBroker(req, into)
}

func doBroker(req *http.Request, into any) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return errors.Join(ErrDown, err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
