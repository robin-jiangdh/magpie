package checkin

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// needsAPIUser matches a site's complaint that the request needs the
// account's user id in the new-api-user header.
var needsAPIUser = regexp.MustCompile(`(?i)new[-_ ]?api[-_ ]?user`)

// alreadyDone matches a check-in reply that says the bonus already
// landed today: that is success, not failure.
var alreadyDone = regexp.MustCompile(`已签到|已经签到|重复签到|already`)

// Probe asks what a site supports. Everything here is unauthenticated on
// purpose: /api/status, anonymous POSTs at the candidate check-in paths,
// and the JWT refresh path.
func Probe(ctx context.Context, baseURL string) (SiteInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	info := SiteInfo{
		BaseURL:      base,
		LoginMethods: []string{},
		QuotaPerUnit: DefaultQuotaPerUnit,
	}

	var statusData map[string]any
	res, err := do(ctx, base, "/api/status", "GET", nil, Creds{}, nil)
	if err != nil {
		return info, errf("无法访问 %s: %v", base, err)
	}
	body, err := bodyMap(res)
	if err != nil {
		return info, err
	}
	if d, ok := body["data"].(map[string]any); ok && len(d) > 0 {
		statusData = d
	}

	// Read flags only if the site actually answered with its status. A WAF
	// answers 200 HTML to every path, which bodyMap turns into a failure
	// map — indistinguishable from "no OAuth", so an unreadable status
	// leaves the tuple empty (absent, not false).
	if statusData != nil {
		methods := []string{"password"}
		for _, k := range oauthFlags {
			if v, _ := statusData[k+"_oauth"].(bool); v {
				methods = append(methods, k)
			}
		}
		info.LoginMethods = methods
		if q, ok := numFloat(statusData["quota_per_unit"]); ok && q > 0 {
			info.QuotaPerUnit = q
		}
		info.Turnstile, _ = statusData["turnstile_check"].(bool)
		info.TurnstileKey, _ = statusData["turnstile_site_key"].(string)
		if e, ok := statusData["checkin_enabled"].(bool); ok {
			info.CheckinEnabled = &e
		}
	}

	// POST-only detection. A GET probe is worthless: GET /api/user/checkin
	// matches the admin route /api/user/:id and answers 200.
	for _, path := range checkinCandidates {
		res, err := do(ctx, base, path, "POST", []byte("{}"), Creds{}, nil)
		if err != nil {
			continue
		}
		// JSON or it did not happen: a WAF challenge answers 200 HTML to
		// every path, which would make every site look like an endpoint one.
		ct := res.Header.Get("Content-Type")
		res.Body.Close()
		if res.StatusCode != 404 && strings.Contains(strings.ToLower(ct), "json") {
			info.CheckinPath = path
			break
		}
	}

	if info.CheckinPath != "" {
		// Does the same path answer GET with a status read? 401 is the
		// proof the route exists and wants a credential; 200 would be the
		// admin route eating the segment.
		if res, err := do(ctx, base, info.CheckinPath, "GET", nil, Creds{}, nil); err == nil {
			ct := res.Header.Get("Content-Type")
			if res.StatusCode == 401 && strings.Contains(strings.ToLower(ct), "json") {
				info.StatusPath = info.CheckinPath
			}
			res.Body.Close()
		}
	}

	// 401 here means the fork authenticates with JWTs, not session cookies.
	if res, err := do(ctx, base, refreshPath, "POST", nil, Creds{}, nil); err == nil {
		ct := res.Header.Get("Content-Type")
		if res.StatusCode != 404 && strings.Contains(strings.ToLower(ct), "json") {
			info.RefreshPath = refreshPath
		}
		res.Body.Close()
	}

	return info, nil
}

// siteRoots derives the site root candidates from a model's API base URL.
// A panel serves its API under /v1 (or similar) and the panel itself at
// the root, so the suffixes come off first and the bare origin is last.
func siteRoots(apiBase string) []string {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimSuffix(u.Path, "/")
	var out []string
	for _, suf := range []string{"/v1beta/openai", "/v1beta", "/v1"} {
		if strings.HasSuffix(path, suf) {
			if r := origin + strings.TrimSuffix(path, suf); !containsStr(out, r) {
				out = append(out, r)
			}
		}
	}
	if !containsStr(out, origin) {
		out = append(out, origin)
	}
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ResolveSite finds the site behind a model's API base URL: it probes each
// root candidate and takes the first one the probe learned anything about.
func ResolveSite(ctx context.Context, apiBase string) (SiteInfo, error) {
	roots := siteRoots(apiBase)
	if len(roots) == 0 {
		return SiteInfo{}, errf("不是有效的站点地址: %q", apiBase)
	}
	var lastErr error
	probed := false
	for _, root := range roots {
		info, err := Probe(ctx, root)
		if err != nil {
			lastErr = err
			continue
		}
		probed = true
		if info.Found() {
			return info, nil
		}
	}
	if lastErr != nil && !probed {
		return SiteInfo{}, lastErr
	}
	return SiteInfo{}, errf("%s 看起来不是签到站（/api/status 无应答）", roots[0])
}
