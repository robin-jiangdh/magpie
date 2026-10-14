// Package checkin signs in to New API style relay sites over plain HTTP
// and performs their daily check-in, ported from the protocol part of
// BingLi37/checkin-panel (panel/newapi.py) via
// ~/workspace/checkin-worker/src/newapi.ts.
//
// Only the pure-HTTP subset is here: site probing, password login,
// access-token / session auth, JWT refresh, and the endpoint and
// login_bonus check-in mechanisms. Browser login, IdP session injection,
// Turnstile minting and the visit mechanism are deliberately absent.
package checkin

import "time"

const (
	// DefaultQuotaPerUnit is a New API site's quota units per USD when the
	// site does not say.
	DefaultQuotaPerUnit = 500_000.0
	// requestTimeout bounds one HTTP round trip to a site.
	requestTimeout = 25 * time.Second
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// checkinCandidates are the paths a daily check-in may live at, most
// specific first: a fork that adds its own check-in usually leaves the
// generic route registered as well — and the generic one may be the
// disabled one, so first-match order picks the working route.
var checkinCandidates = []string{
	"/api/user/sota-agent-checkin",
	"/api/user/checkin",
	"/api/user/check_in",
	"/api/user/sign_in",
	"/api/user/clock_in",
}

// refreshPath trades a new_api_refresh cookie for an access token on JWT
// forks; refreshCookie is the cookie's name.
const refreshPath = "/api/user/auth/refresh"
const refreshCookie = "new_api_refresh"

// oauthFlags are the login methods a site's /api/status may advertise
// besides the password one, each as "<flag>_oauth".
var oauthFlags = []string{"linuxdo", "github", "oidc", "telegram", "wechat"}

// LoginMethod values a check-in can use.
const (
	LoginPassword    = "password"
	LoginAccessToken = "access_token"
	LoginSession     = "session"
)

// Mechanism values a check-in can run as; MechanismAuto probes the site.
const (
	MechanismAuto       = "auto"
	MechanismEndpoint   = "endpoint"
	MechanismLoginBonus = "login_bonus"
)

// SiteInfo is what Probe learned about a site. Zero values mean "the site
// did not say", not "no".
type SiteInfo struct {
	BaseURL        string   `json:"baseUrl"`
	LoginMethods   []string `json:"loginMethods"`
	QuotaPerUnit   float64  `json:"quotaPerUnit"`
	Turnstile      bool     `json:"turnstile"`
	TurnstileKey   string   `json:"turnstileKey,omitempty"`
	CheckinPath    string   `json:"checkinPath,omitempty"`
	RefreshPath    string   `json:"refreshPath,omitempty"`
	StatusPath     string   `json:"statusPath,omitempty"`
	CheckinEnabled *bool    `json:"checkinEnabled"`
}

// Mechanism reports the check-in mechanism the site takes: a check-in
// route means endpoint, otherwise a fresh login is the check-in.
func (s SiteInfo) Mechanism() string {
	if s.CheckinPath != "" {
		return MechanismEndpoint
	}
	return MechanismLoginBonus
}

// Found says the probe learned anything at all about the site.
func (s SiteInfo) Found() bool {
	return len(s.LoginMethods) > 0 || s.CheckinEnabled != nil ||
		s.CheckinPath != "" || s.RefreshPath != ""
}

// Login is one check-in attempt's identity.
type Login struct {
	LoginMethod string
	Username    string
	Password    string
	AccessToken string
	Session     string
	APIUser     string
}

// Creds is the credential a read-only call authenticates with.
type Creds struct {
	Session     string
	AccessToken string
	APIUser     string
}

// Outcome is what one check-in attempt did. It never carries a credential
// back to the caller; a fresh session worth persisting is Session.
type Outcome struct {
	Success     bool    `json:"success"`
	CheckedIn   bool    `json:"checkedIn"`
	BeforeQuota *float64 `json:"beforeQuota,omitempty"`
	AfterQuota  *float64 `json:"afterQuota,omitempty"`
	Awarded     *float64 `json:"awarded,omitempty"` // USD the site itself said it just granted
	Error       string   `json:"error,omitempty"`
	Session     string   `json:"-"` // fresh session worth persisting, never serialized
	APIUser     string   `json:"apiUser,omitempty"`
	Username    string   `json:"username,omitempty"`
}

// Delta is after minus before, when both are known.
func (o Outcome) Delta() *float64 {
	if o.BeforeQuota == nil || o.AfterQuota == nil {
		return nil
	}
	d := *o.AfterQuota - *o.BeforeQuota
	d = float64(int(d*100+0.5)) / 100
	return &d
}

// Gain prefers what the site reported over the measured delta.
func (o Outcome) Gain() *float64 {
	if o.Awarded != nil {
		return o.Awarded
	}
	return o.Delta()
}

// CheckinStatusInfo is a read-only look at a site's check-in state.
type CheckinStatusInfo struct {
	Today        *bool    `json:"today,omitempty"`
	AwardedToday *float64 `json:"awardedToday,omitempty"`
	Reward       *float64 `json:"reward,omitempty"`
	Total        *int     `json:"total,omitempty"`
}
