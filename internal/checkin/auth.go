package checkin

import (
	"context"
	"encoding/json"
	"fmt"
)

// selfGet reads /api/user/self with the credential: who it is, or why not.
func selfGet(ctx context.Context, base string, creds Creds, j *jar) (map[string]any, string) {
	res, err := do(ctx, base, "/api/user/self", "GET", nil, creds, j)
	if err != nil {
		return nil, fmt.Sprintf("无法访问站点: %v", err)
	}
	body, err := bodyMap(res)
	if err != nil {
		return nil, err.Error()
	}
	if res.StatusCode == 200 {
		if ok, _ := body["success"].(bool); ok {
			if d, ok := body["data"].(map[string]any); ok {
				return d, ""
			}
			return map[string]any{}, ""
		}
	}
	msg := failMessage(body, res)
	if creds.APIUser == "" && needsAPIUser.MatchString(msg) {
		return nil, msg + "（凭据本身没问题，但这个站点还要账号的用户 id，请填 API User）"
	}
	return nil, msg
}

// Authenticate is selfGet without a jar: (the user behind a credential,
// why it failed).
func Authenticate(ctx context.Context, base string, creds Creds) (map[string]any, string) {
	return selfGet(ctx, base, creds, nil)
}

// Whoami returns the user behind a credential, or nil.
func Whoami(ctx context.Context, base string, creds Creds) map[string]any {
	d, _ := Authenticate(ctx, base, creds)
	return d
}

// Balance reads the account's remaining quota in USD.
func Balance(ctx context.Context, base string, quotaPerUnit float64, creds Creds) *float64 {
	return usdOf(Whoami(ctx, base, creds), quotaPerUnit)
}

// passwordLogin trades a username and password for a session in the jar.
// It returns the login response's data and the account id the site
// adopted (some forks validate new-api-user against the session).
func passwordLogin(ctx context.Context, base string, j *jar, login Login) (map[string]any, string, string) {
	if login.Username == "" || login.Password == "" {
		return nil, "缺少用户名/密码", ""
	}
	body, _ := json.Marshal(map[string]string{
		"username": login.Username,
		"password": login.Password,
	})
	res, err := do(ctx, base, "/api/user/login", "POST", body, Creds{APIUser: login.APIUser}, j)
	if err != nil {
		return nil, fmt.Sprintf("无法访问站点: %v", err), ""
	}
	bm, err := bodyMap(res)
	if err != nil {
		return nil, err.Error(), ""
	}
	if ok, _ := bm["success"].(bool); !ok {
		return nil, failMessage(bm, res), ""
	}
	data, _ := bm["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	var apiUser string
	if id, ok := data["id"]; ok && id != nil {
		apiUser = fmt.Sprintf("%v", id)
	}
	return data, "", apiUser
}

// RefreshAccess trades a new_api_refresh cookie for an access token. It
// returns the token, a rotated refresh cookie ("" when the one spent is
// still live — returning it would kill the account on the next run),
// and the user behind it.
func RefreshAccess(ctx context.Context, base, refresh string) (token, rotated string, user map[string]any) {
	j := newJar()
	j.set(refreshCookie, refresh)
	res, err := do(ctx, base, refreshPath, "POST", nil, Creds{}, j)
	if err != nil {
		return "", "", map[string]any{}
	}
	body, err := bodyMap(res)
	if err != nil {
		return "", "", map[string]any{}
	}
	if ok, _ := body["success"].(bool); !ok {
		return "", "", map[string]any{}
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	if t, ok := data["access_token"].(string); ok {
		token = t
	}
	if r := j.value(refreshCookie); r != "" && r != refresh {
		rotated = r
	}
	if u, ok := data["user"].(map[string]any); ok {
		user = u
	} else {
		user = map[string]any{}
	}
	return token, rotated, user
}
