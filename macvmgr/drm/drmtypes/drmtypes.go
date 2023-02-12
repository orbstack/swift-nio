package drmtypes

import "time"

const (
	CurrentVersion = 1
	CurrentKeyID   = "1"
)

type State int

const (
	StateInvalid State = iota
	StateValid
)

type EntitlementTier int

const (
	EntitlementTierNone EntitlementTier = iota
	EntitlementTierPersonal
	EntitlementTierBusiness
)

type EntitlementType int

const (
	EntitlementTypeNone EntitlementType = iota
	EntitlementTypeSubMonthly
	EntitlementTypeSubYearly
)

type Identifiers struct {
	DeviceID  string
	InstallID string
	ClientID  string
}

type JwtClaims struct {
	// user
	UserID          string          `json:"sub"`
	EntitlementTier EntitlementTier `json:"ent"`
	EntitlementType EntitlementType `json:"etp"`

	// app
	AppName    string     `json:"aud"`
	AppVersion AppVersion `json:"ver"`

	// device
	// per-machine, portable across installs and users
	DeviceID string `json:"did"`
	// per-install, portable across machines
	InstallID string `json:"iid"`
	// per-user, portable across installs
	ClientID string `json:"cid"`

	// server
	Issuer string `json:"iss"`

	// security
	IssuedAt int64 `json:"iat"`
	// license end + grace period. this is when the app stops working
	ExpiresAt  int64 `json:"exp"`
	NotBefore  int64 `json:"nbf"`
	DrmVersion uint8 `json:"dvr"`

	// UX
	// license end - warn period. this is when the app starts showing a warning
	WarnAt int64 `json:"war"`
	// license end. this is when the app says it expires and gets more aggressive, but still works until grace period is over (expiresAt)
	LicenseEndsAt int64 `json:"lxp"`
}

func (c *JwtClaims) Valid() error {
	return nil
}

type ClaimInfo struct {
	UserID   string
	IssuedAt time.Time

	ExpiresAt     time.Time
	LicenseEndsAt time.Time
	WarnAt        time.Time

	EntitlementTier EntitlementTier
	EntitlementType EntitlementType
}

type Result struct {
	State            State
	EntitlementToken string
	RefreshToken     string
	ClaimInfo        *ClaimInfo
	CheckedAt        time.Time
}

type AppVersion struct {
	Code int    `json:"code"`
	Git  string `json:"git"`
}

type EntitlementRequest struct {
	RefreshToken string      `json:"refresh_token"`
	Identifiers  Identifiers `json:"identifiers"`
	AppVersion   AppVersion  `json:"app_version"`
	ClientTime   time.Time   `json:"client_time"`
}

type EntitlementResponse struct {
	State            State     `json:"state"`
	EntitlementToken string    `json:"entitlement_token"`
	RefreshToken     string    `json:"refresh_token"`
	CheckedAt        time.Time `json:"checked_at"`
}
