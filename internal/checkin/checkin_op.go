package checkin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// failOutcome builds a failed Outcome, carrying the session out even in
// failure: a JWT fork may have rotated its refresh cookie a moment ago,
// and dropping it bricks the account.
func failOutcome(err string, session string) Outcome {
	return Outcome{Success: false, Error: err, Session: session}
}

// CheckIn does the daily check-in over HTTP. It never launches a browser.
// mechanism is MechanismAuto (probe the site), MechanismEndpoint or
// MechanismLoginBonus.
func CheckIn(ctx context.Context, login Login, site SiteInfo, mechanism string) Outcome {
	l := login

	if site.RefreshPath != "" && l.Session != "" && l.AccessToken == "" {
		// A JWT fork stores no session: what sits in the session field is
		// a refresh cookie, and it has to be spent for an access token
		// first.
		token, rotated, user := RefreshAccess(ctx, site.BaseURL, l.Session)
		if token != "" {
			l.AccessToken = token
			if rotated != "" {
				l.Session = rotated // rotated — must be persisted
			}
			if l.APIUser == "" {
				if id, ok := user["id"]; ok && id != nil {
					l.APIUser = fmt.Sprintf("%v", id)
				}
			}
		} else if l.Username == "" || l.Password == "" {
			return failOutcome("refresh 凭据已失效：到站点重新登录，复制新的 new_api_refresh cookie 再粘进来", l.Session)
		}
	}

	if site.Turnstile {
		// Turnstile tokens are minted in a browser; there is none here.
		return failOutcome("站点开启了 Turnstile 验证，纯 HTTP 签到无法通过人机验证", l.Session)
	}

	if mechanism == "" {
		mechanism = MechanismAuto
	}
	if mechanism == MechanismAuto {
		mechanism = site.Mechanism()
	}
	if mechanism == MechanismEndpoint {
		if site.CheckinPath == "" {
			return failOutcome("站点没有签到接口（探测未发现签到路由）", l.Session)
		}
		return checkInEndpoint(ctx, l, site)
	}
	return checkInLoginBonus(ctx, l, site)
}

// checkInEndpoint POSTs the site's check-in route with the credential.
func checkInEndpoint(ctx context.Context, login Login, site SiteInfo) Outcome {
	j := newJar()
	creds := Creds{
		Session:     login.Session,
		AccessToken: login.AccessToken,
		APIUser:     login.APIUser,
	}
	// Seed the jar from a pasted session so the server-set cookie logic
	// below can tell "server rotated it" apart from "nothing came back".
	if login.Session != "" && login.AccessToken == "" {
		j.set("session", login.Session)
	}

	adopted := login.APIUser

	if login.Session == "" && login.AccessToken == "" {
		data, errMsg, apiUser := passwordLogin(ctx, site.BaseURL, j, login)
		_ = data
		if errMsg != "" {
			return failOutcome("登录失败: "+errMsg, login.Session)
		}
		if apiUser != "" {
			adopted = apiUser
		}
	}

	// Authenticated reads go through the jar: the session cookie lives
	// there so a server-side rotation is picked up automatically.
	hdr := func() Creds { return Creds{AccessToken: creds.AccessToken, APIUser: adopted} }
	before, beforeErr := selfGet(ctx, site.BaseURL, hdr(), j)

	if before == nil && login.Username != "" && login.Password != "" {
		// Dead session, or a fork demanding new-api-user we do not have
		// yet. A password login fixes both: it adopts the id from its
		// response.
		j.clear()
		data, errMsg, apiUser := passwordLogin(ctx, site.BaseURL, j, login)
		_ = data
		if errMsg != "" {
			return failOutcome("登录失败: "+errMsg, login.Session)
		}
		if apiUser != "" {
			adopted = apiUser
		}
		before, beforeErr = selfGet(ctx, site.BaseURL, Creds{AccessToken: creds.AccessToken, APIUser: adopted}, j)
		if before == nil && login.Password == "" {
			return failOutcome("凭据无效: "+beforeErr, login.Session)
		}
	}

	res, err := do(ctx, site.BaseURL, site.CheckinPath, "POST", []byte("{}"), Creds{AccessToken: creds.AccessToken, APIUser: adopted}, j)
	if err != nil {
		return failOutcome(fmt.Sprintf("签到请求失败: %v", err), j.value("session"))
	}
	body, err := bodyMap(res)
	if err != nil {
		return failOutcome(err.Error(), j.value("session"))
	}
	var granted map[string]any
	if d, ok := body["data"].(map[string]any); ok {
		granted = d
	}
	after, _ := selfGet(ctx, site.BaseURL, Creds{AccessToken: creds.AccessToken, APIUser: adopted}, j)

	// Some forks report the result: {quota_awarded, current_quota}.
	// current_quota is the balance *after* the grant, straight from the
	// site — beats a second read that could race with usage.
	var afterQuota *float64
	if granted != nil {
		afterQuota = usdRaw(granted["current_quota"], site.QuotaPerUnit)
	}
	if afterQuota == nil {
		afterQuota = usdOf(after, site.QuotaPerUnit)
	}

	out := Outcome{
		Success:     false,
		CheckedIn:   false,
		BeforeQuota: usdOf(before, site.QuotaPerUnit),
		AfterQuota:  afterQuota,
		Session:     j.value("session"),
		AccessToken: login.AccessToken,
		APIUser:     adopted,
		Awarded:     usdRaw(granted["quota_awarded"], site.QuotaPerUnit),
	}
	if out.Session == "" {
		out.Session = login.Session
	}
	if ok, _ := body["success"].(bool); ok {
		out.Success = true
		out.CheckedIn = true
		return out
	}
	msg := failMessage(body, res)
	if alreadyDone.MatchString(msg) {
		// The bonus already landed today: success, not failure.
		out.Success = true
		out.CheckedIn = false
		return out
	}
	out.Error = msg
	return out
}

// checkInLoginBonus: no check-in route — a fresh login IS the check-in.
func checkInLoginBonus(ctx context.Context, login Login, site SiteInfo) Outcome {
	if login.Username == "" || login.Password == "" {
		return failOutcome(`该站点靠"重新登录"发放额度，而当前凭据无法重新登录：请填写账号密码`, login.Session)
	}
	j := newJar()
	if login.Session != "" {
		j.set("session", login.Session)
	}
	before, _ := selfGet(ctx, site.BaseURL, Creds{APIUser: login.APIUser}, j)
	j.clear() // a fresh login replaces the old session, it does not stack
	data, errMsg, apiUser := passwordLogin(ctx, site.BaseURL, j, login)
	if errMsg != "" {
		return Outcome{
			Success:     false,
			Error:       "登录失败: " + errMsg,
			BeforeQuota: usdOf(before, site.QuotaPerUnit),
			Session:     login.Session,
		}
	}
	after, _ := selfGet(ctx, site.BaseURL, Creds{APIUser: apiUser}, j)
	afterQuota := usdOf(after, site.QuotaPerUnit)
	if afterQuota == nil {
		afterQuota = usdOf(data, site.QuotaPerUnit)
	}
	var username string
	if u, ok := data["username"].(string); ok {
		username = u
	}
	if apiUser == "" {
		if id, ok := data["id"]; ok && id != nil {
			apiUser = fmt.Sprintf("%v", id)
		}
	}
	return Outcome{
		Success: true,
		// The login response's checked_in is the only honest success
		// signal — quota may legitimately not move if the bonus already
		// landed today.
		CheckedIn:   ifBool(data["checked_in"]),
		BeforeQuota: usdOf(before, site.QuotaPerUnit),
		AfterQuota:  afterQuota,
		Session:     j.value("session"),
		APIUser:     apiUser,
		Username:    username,
	}
}

func ifBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// CheckinStatus GETs a fork's check-in status route. It performs no
// check-in; its whole contract is "cannot say" on any trouble.
func CheckinStatus(ctx context.Context, site SiteInfo, creds Creds) CheckinStatus {
	empty := CheckinStatus{}
	if site.StatusPath == "" {
		return empty
	}
	now := time.Now()
	month := fmt.Sprintf("%d-%02d", now.Year(), int(now.Month()))
	res, err := do(ctx, site.BaseURL, site.StatusPath+"?month="+month, "GET", nil, creds, nil)
	if err != nil {
		return empty
	}
	body, err := bodyMap(res)
	if err != nil {
		return empty
	}
	if ok, _ := body["success"].(bool); !ok {
		return empty
	}
	d, ok := body["data"].(map[string]any)
	if !ok {
		return empty
	}
	stats, ok := d["stats"].(map[string]any)
	if !ok {
		stats = d
	}
	stamp := fmt.Sprintf("%s-%02d", month, now.Day())
	awarded := stats["quota_awarded_today"]
	if awarded == nil {
		if records, ok := stats["records"].([]any); ok {
			for _, r := range records {
				if rm, ok := r.(map[string]any); ok {
					if s, _ := rm["checkin_date"].(string); strings.HasPrefix(s, stamp) {
						awarded = rm["quota_awarded"]
						break
					}
				}
			}
		}
	}
	out := CheckinStatus{
		AwardedToday: usdRaw(awarded, site.QuotaPerUnit),
	}
	if t, ok := stats["checked_in_today"].(bool); ok {
		out.Today = &t
	}
	if r, ok := numFloat(d["reward_credits"]); ok {
		// reward_credits is already a display amount, not raw quota units.
		out.Reward = &r
	}
	if n, ok := numFloat(stats["total_checkins"]); ok {
		i := int(n)
		out.Total = &i
	}
	return out
}

// LastCheckinAt returns when the site's own quota log last recorded a
// check-in bonus, or nil when it cannot say. New API's log type 4 is the
// system type: 1 topup, 2 consume, 3 manage, 4 system, 5 error.
func LastCheckinAt(ctx context.Context, base string, creds Creds) *int64 {
	res, err := do(ctx, base, "/api/log/self?p=0&page_size=20&type=4", "GET", nil, creds, nil)
	if err != nil {
		return nil
	}
	body, err := bodyMap(res)
	if err != nil {
		return nil
	}
	if ok, _ := body["success"].(bool); !ok {
		return nil
	}
	var items []any
	if d, ok := body["data"].(map[string]any); ok {
		items, _ = d["items"].([]any)
	} else {
		items, _ = body["data"].([]any)
	}
	var best *int64
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		content, _ := m["content"].(string)
		if !checkinLog.MatchString(content) {
			continue
		}
		if ts, ok := numFloat(m["created_at"]); ok {
			v := int64(ts)
			if best == nil || v > *best {
				best = &v
			}
		}
	}
	return best
}
