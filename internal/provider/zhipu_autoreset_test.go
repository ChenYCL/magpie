package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A fake GLM plan panel: /list tells the cards, /use spends one and says
// which resetType it was asked with.
func zhipuResetServer(t *testing.T, weekly int) (*httptest.Server, *[]string) {
	t.Helper()
	var used []string
	cards := func() string {
		var ws []string
		for i := range weekly {
			ws = append(ws, `{"recordId":`+strconv.Itoa(i+1)+`,"grantType":"GLOBAL","expireTime":"2026-12-22 23:59:59","available":true}`)
		}
		return `{"code":200,"msg":"ok","data":{"fiveHourResets":[],"weekResets":[` + strings.Join(ws, ",") + `]}}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/customer-package-reset/list"):
			w.Write([]byte(cards()))
		case strings.HasSuffix(r.URL.Path, "/customer-package-reset/use"):
			var in struct {
				ResetType string `json:"resetType"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if weekly == 0 {
				w.Write([]byte(`{"code":400,"msg":"No reset attempts are available","success":false}`))
				return
			}
			weekly--
			used = append(used, in.ResetType)
			w.Write([]byte(`{"code":200,"msg":"ok","data":1,"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &used
}

func TestZhipuUseResetSpendsOneCard(t *testing.T) {
	srv, used := zhipuResetServer(t, 2)
	p := Provider{ID: "zai-codingplan", Chat: srv.URL + "/api/coding/paas/v4", Key: "glm-key"}
	out, err := useZhipuReset(context.Background(), "zai-codingplan", p, "glm-key", ResetUsedZhipuHand)
	if err != nil || out.Code != "reset" {
		t.Fatalf("use: %+v %v", out, err)
	}
	if len(*used) != 1 || (*used)[0] != "WEEK" {
		t.Fatalf("spent %v, want one WEEK", *used)
	}
	r := zhipuPersonalResets(context.Background(), srv.URL, "glm-key")
	if r == nil || r.Weekly != 1 {
		t.Fatalf("cards left: %+v, want 1", r)
	}
}

func TestZhipuUseResetNoneLeft(t *testing.T) {
	srv, used := zhipuResetServer(t, 0)
	p := Provider{ID: "zai-codingplan", Chat: srv.URL, Key: "glm-key"}
	out, err := useZhipuReset(context.Background(), "zai-codingplan", p, "glm-key", ResetUsedZhipuHand)
	if err != nil || out.Code != "no_credit" {
		t.Fatalf("use with none: %+v %v", out, err)
	}
	if len(*used) != 0 {
		t.Fatalf("spent %v with none held", *used)
	}
}

func TestZhipuAutoResetOffByDefault(t *testing.T) {
	if ZhipuAutoReset("zai-codingplan") {
		t.Fatal("a plan spends a card by itself before anyone turns it on")
	}
	out, err := AutoUseZhipuReset(context.Background(), "zai-codingplan")
	if err != nil || out.Code != "" {
		t.Fatalf("off still spent: %+v %v", out, err)
	}
}

// The week being used up is what makes a card spendable, and only the
// week: a five-hour window at 100% never is.
func TestWeekUsedUpIgnoresFiveHours(t *testing.T) {
	soon := time.Now().Add(2 * time.Hour)
	week := time.Now().Add(48 * time.Hour)
	ws := []QuotaWindow{
		{Name: "5 hours", Used: 100, Span: 5 * time.Hour, ResetsAt: &soon},
		{Name: "7 days", Used: 99, Span: 7 * 24 * time.Hour, ResetsAt: &week},
	}
	if weekUsedUp(ws, time.Now()) != nil {
		t.Fatal("a week not yet used up, or five hours full, spent a card")
	}
	ws[1].Used = 100
	if got := weekUsedUp(ws, time.Now()); got == nil || !got.Equal(week) {
		t.Fatalf("week used up: %v", got)
	}
}
