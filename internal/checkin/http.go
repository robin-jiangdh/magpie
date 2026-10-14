package checkin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// jar is a minimal cookie jar: a name to value map. Go's http.Client has
// no jar here on purpose — a site's rotation is told apart from "nothing
// came back" only when the jar is seeded and read explicitly.
type jar struct {
	m map[string]string
}

func newJar() *jar { return &jar{m: map[string]string{}} }

func (j *jar) set(name, value string) { j.m[name] = value }

func (j *jar) clear() { j.m = map[string]string{} }

// header renders the jar as one Cookie header value.
func (j *jar) header() string {
	if len(j.m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(j.m))
	for k, v := range j.m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// absorb takes the Set-Cookie headers' value for name into the jar and
// returns it.
func (j *jar) absorb(h http.Header, name string) string {
	var found string
	for _, line := range h.Values("Set-Cookie") {
		// one cookie per header line: name=value; attrs...
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		n := strings.TrimSpace(line[:eq])
		if !strings.EqualFold(n, name) {
			continue
		}
		v := line[eq+1:]
		if i := strings.Index(v, ";"); i >= 0 {
			v = v[:i]
		}
		v = strings.TrimSpace(v)
		j.m[name] = v
		found = v
	}
	return found
}

// value reads one cookie out of the jar's rendered header.
func (j *jar) value(name string) string {
	h := j.header()
	if h == "" {
		return ""
	}
	for _, part := range strings.Split(h, ";") {
		part = strings.TrimSpace(part)
		if k, v, ok := strings.Cut(part, "="); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// do sends one request to base+path. Auth goes in the headers the way the
// sites expect: the access token raw in Authorization, the API user id in
// new-api-user, and the session from the jar (or the bare credential).
func do(ctx context.Context, base, path, method string, body []byte, creds Creds, j *jar) (*http.Response, error) {
	base = strings.TrimRight(base, "/")
	r, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", userAgent)
	// Some forks 403 AUTH_ORIGIN_FORBIDDEN without this; a browser sets it
	// on its own and fetch does not.
	r.Header.Set("Origin", base)
	if creds.AccessToken != "" {
		r.Header.Set("Authorization", creds.AccessToken)
	}
	if creds.APIUser != "" {
		r.Header.Set("new-api-user", creds.APIUser)
	}
	if j != nil {
		if h := j.header(); h != "" {
			r.Header.Set("Cookie", h)
		}
	} else if creds.Session != "" {
		r.Header.Set("Cookie", "session="+creds.Session)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	res, err := http.DefaultClient.Do(r.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if j != nil {
		j.absorb(res.Header, "session")
		j.absorb(res.Header, refreshCookie)
	}
	return res, nil
}

// bodyMap reads a JSON object body. A reply that is not JSON is the error
// itself — a WAF challenge answers 200 HTML to every path.
func bodyMap(res *http.Response) (map[string]any, error) {
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "json") {
		return map[string]any{
			"success": false,
			"message": fmt.Sprintf("HTTP %d（非 JSON 响应）", res.StatusCode),
		}, nil
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{
			"success": false,
			"message": fmt.Sprintf("HTTP %d（非 JSON 响应）", res.StatusCode),
		}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{"success": false, "message": string(b[:min(len(b), 200)])}, nil
}

// failMessage picks the human reading of a failed reply.
func failMessage(body map[string]any, res *http.Response) string {
	msg, _ := body["message"]
	if msg == nil {
		msg, _ = body["error"]
	}
	if msg == nil {
		return fmt.Sprintf("HTTP %d", res.StatusCode)
	}
	if m, ok := msg.(map[string]any); ok {
		if inner, ok := m["message"]; ok {
			msg = inner
		}
	}
	return fmt.Sprintf("%v", msg)
}

// numFloat reads a JSON number as float64.
func numFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// usdRaw converts raw quota units to USD with the site's divisor.
func usdRaw(quota any, perUnit float64) *float64 {
	q, ok := numFloat(quota)
	if !ok {
		return nil
	}
	if perUnit <= 0 {
		perUnit = DefaultQuotaPerUnit
	}
	v := float64(int(q/perUnit*100+0.5)) / 100
	return &v
}

// usdOf reads the quota field of a /api/user/self style data map.
func usdOf(data map[string]any, perUnit float64) *float64 {
	if data == nil {
		return nil
	}
	return usdRaw(data["quota"], perUnit)
}

func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
