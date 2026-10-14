package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/robin-jiangdh/magpie/internal/provider"
)

// #2008 (Gaudi-XIX): a provider's own rest cap (`magpie provider rest-cap`)
// is where a key that keeps failing, or keeps being rate limited as soon
// as its rest is over, stops doubling: past the gateway's 10 and 30
// minutes for a free key that is down for hours, or short of them.
func TestOwnRestCap(t *testing.T) {
	s := &Server{}
	near := func(d, want time.Duration) bool { return d > want-5*time.Second && d <= want }
	ladder := func(name string, capMin, status int, body string, want ...time.Duration) {
		t.Helper()
		c := candidate{p: provider.Provider{ID: "capped-" + name, Key: "k", RestCap: capMin}, model: "m", rest: "capped-" + name + "#k"}
		id := c.restKey()
		for i, w := range want {
			r := s.restAfter(c, status, http.Header{}, []byte(body))
			if !near(time.Until(r.Until), w) {
				t.Fatalf("%s #%d: rests %v by %s (%s), want %v", name, i+1, time.Until(r.Until), r.By, r.Why, w)
			}
			restingUntil.Lock()
			restingUntil.m[id] = time.Now().Add(-time.Second)
			restingUntil.Unlock()
		}
	}
	rate := `{"error":{"message":"Too many requests","type":"rate_limit_error"}}`
	fail := `{"error":{"message":"upstream connect error","type":"api_error"}}`
	m := time.Minute
	ladder("rate-long", 240, 429, rate, m, 2*m, 4*m, 8*m, 16*m, 32*m, 64*m, 128*m, 240*m, 240*m)
	ladder("rate-short", 3, 429, rate, m, 2*m, 3*m, 3*m)
	ladder("rate-own", 0, 429, rate, m, 2*m, 4*m, 8*m, 16*m, 30*m, 30*m)
	ladder("fail-long", 120, 502, fail, m, 2*m, 4*m, 8*m, 16*m, 32*m, 64*m, 120*m)
	ladder("fail-own", 0, 502, fail, m, 2*m, 4*m, 8*m, 10*m, 10*m)
}

func TestRestCapOr(t *testing.T) {
	if d := (provider.Provider{RestCap: 90}).RestCapOr(time.Minute); d != 90*time.Minute {
		t.Error(d)
	}
	if d := (provider.Provider{}).RestCapOr(time.Minute); d != time.Minute {
		t.Error(d)
	}
	if d := (provider.Provider{RestCap: 99999999}).RestCapOr(time.Minute); d != provider.MaxQuotaRest*time.Minute {
		t.Error(d)
	}
}
