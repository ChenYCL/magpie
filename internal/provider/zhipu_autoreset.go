package provider

// Spending a GLM Coding Plan's weekly reset card by itself, for the keys
// the user said may: one card starts the week's window again when a
// request finds it used up and nobody else can answer, and one is spent
// when the soonest card is within a day of running out unused. One a week
// at most, so a heavy week doesn't eat the pile. What was spent is kept in
// zhipu-autoreset.json, so a restart doesn't spend another.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

// Why a GLM reset card was used, kept beside the Codex ones in
// codex-resets-used.json (the same file the card reads): the week's window
// used up and nobody else left, the soonest card about to run out, or by
// hand.
const (
	ResetUsedZhipuWeek     = "zhipu-week"
	ResetUsedZhipuExpiring = "zhipu-expiring"
	ResetUsedZhipuHand     = "zhipu-hand"
)

// zhipuResetLead is how close to running out a card must be before one is
// spent so it isn't lost. A day, not Codex's half hour: a GLM card lasts
// months and is read on the quota tick, so a short lead could be missed.
const zhipuResetLead = 24 * time.Hour

// ZhipuAutoReset says whether the GLM Coding Plan key (a provider's id, or
// "id@key name" when it holds several) spends a weekly reset card by
// itself. Off unless the user turned it on.
func ZhipuAutoReset(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return id != "" && slices.Contains(settings.Load().ZhipuAutoReset, id)
}

// SetZhipuAutoReset turns that on or off for id.
func SetZhipuAutoReset(id string, on bool) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return errors.New("a GLM Coding Plan key is needed")
	}
	s := settings.Load()
	s.ZhipuAutoReset = slices.DeleteFunc(slices.Clone(s.ZhipuAutoReset), func(u string) bool { return u == id })
	if on {
		s.ZhipuAutoReset = append(s.ZhipuAutoReset, id)
	}
	return settings.Save(s)
}

// zhipuResetKey is the id a plan's key is kept by: the provider's alone
// when it holds one key, "id@name" when several, matching the card's User.
func zhipuResetKey(p Provider, keyName string) string {
	if len(p.Keys) > 0 {
		return p.ID + "@" + keyName
	}
	return p.ID
}

var zhipuAutoReset = autoResets{file: "zhipu-autoreset.json"}

// AutoUseZhipuReset spends one weekly reset card of the GLM Coding Plan
// key id if the user turned that on, its week is used up, it holds one,
// and none was spent by itself in this week yet. Code is "" when none was
// tried; the caller asks again only on "reset".
func AutoUseZhipuReset(ctx context.Context, id string) (ResetOutcome, error) {
	if !ZhipuAutoReset(id) {
		return ResetOutcome{}, nil
	}
	p, key, ok := zhipuKeyOf(id)
	if !ok {
		return ResetOutcome{}, fmt.Errorf("no GLM Coding Plan key %q", id)
	}
	return zhipuAutoReset.use(id, func(now time.Time) (*time.Time, bool, error) {
		_, ws, _, err := planKeyWindows(ctx, p, zhipuPlanSource(p), key)
		if err != nil {
			return nil, false, err
		}
		r := zhipuPersonalResets(ctx, zcodeRoot(p.Chat), key)
		return weekUsedUp(ws, now), r != nil && r.Weekly > 0, nil
	}, func() (ResetOutcome, error) {
		return useZhipuReset(ctx, id, p, key, ResetUsedZhipuWeek)
	})
}

// SpendExpiringZhipuResets spends one weekly card of every GLM key the
// user lets, whose soonest card runs out within a day — so a card isn't
// lost for never being used. One a week at most, the same ledger as the
// week's. Best effort: a failure is logged and the next key is tried.
func SpendExpiringZhipuResets(ctx context.Context) {
	for _, id := range settings.Load().ZhipuAutoReset {
		p, key, ok := zhipuKeyOf(id)
		if !ok {
			continue
		}
		r := zhipuPersonalResets(ctx, zcodeRoot(p.Chat), key)
		if r == nil || r.Weekly == 0 || r.Until == nil || time.Until(*r.Until) > zhipuResetLead {
			continue
		}
		out, err := zhipuAutoReset.use(id, func(now time.Time) (*time.Time, bool, error) {
			until := now.Add(autoResetWeek)
			return &until, true, nil
		}, func() (ResetOutcome, error) {
			return useZhipuReset(ctx, id, p, key, ResetUsedZhipuExpiring)
		})
		switch {
		case err != nil:
			log.Printf("glm reset for %s not used, a card runs out at %s: %v", id, r.Until.Format(time.RFC3339), err)
		case out.Code == "reset":
			log.Printf("glm reset used for %s, a card was about to run out at %s", id, r.Until.Format(time.RFC3339))
		}
	}
}

// UseZhipuReset spends one weekly card of id by hand. It can't be undone.
func UseZhipuReset(ctx context.Context, id string) (ResetOutcome, error) {
	p, key, ok := zhipuKeyOf(id)
	if !ok {
		return ResetOutcome{}, fmt.Errorf("no GLM Coding Plan key %q", id)
	}
	return useZhipuReset(ctx, id, p, key, ResetUsedZhipuHand)
}

// useZhipuReset spends one weekly card and keeps the use under id. The
// vendor picks the card; it spends the one that runs out first.
func useZhipuReset(ctx context.Context, id string, p Provider, key, why string) (ResetOutcome, error) {
	var spent any
	err := zcodeCall(ctx, "POST", zcodeRoot(p.Chat)+"/api/biz/customer-package-reset/use", key,
		map[string]string{"targetType": "PERSONAL", "resetType": "WEEK"}, &spent)
	if err != nil {
		if strings.Contains(err.Error(), "No reset attempts") {
			return ResetOutcome{Code: "no_credit"}, nil
		}
		return ResetOutcome{}, err
	}
	out := ResetOutcome{Code: "reset", Windows: 1}
	noteResetUsed(id, why, out)
	subscriptionUsageCache.Lock()
	subscriptionUsageCache.at = time.Time{}
	subscriptionUsageCache.data = nil
	subscriptionUsageCache.Unlock()
	toldUsage()
	return out, nil
}

// zhipuKeyOf finds the GLM Coding Plan key id names: a provider's id, or
// "id@key name".
func zhipuKeyOf(id string) (Provider, string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	pid, name, named := strings.Cut(id, "@")
	for _, p := range All() {
		if !strings.EqualFold(p.ID, pid) || !zhipuPlan(p) {
			continue
		}
		if !named {
			return p, p.Key, p.Key != ""
		}
		if strings.EqualFold(p.KeyName, name) {
			return p, p.Key, true
		}
		for _, k := range p.Keys {
			if strings.EqualFold(k.Name, name) && k.Key != "" {
				return p, k.Key, true
			}
		}
	}
	return Provider{}, "", false
}

// zhipuPlan says p's key is a GLM Coding Plan's: its quota is read at the
// plan's own endpoint.
func zhipuPlan(p Provider) bool {
	src, ok := planQuotaSourceOf(p)
	return ok && strings.HasSuffix(src.url, "/api/monitor/usage/quota/limit")
}

// zhipuPlanSource is the quota source of a GLM plan's key.
func zhipuPlanSource(p Provider) planQuotaSource {
	src, _ := planQuotaSourceOf(p)
	return src
}
