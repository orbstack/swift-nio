package drmtypes

import "time"

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
	Raw string `json:"raw"`
}

type EntitlementRequest struct {
	RefreshToken string
	Identifiers  *Identifiers
	AppVersion   AppVersion
}

type EntitlementResponse struct {
	State            State
	EntitlementToken string
	RefreshToken     string
	CheckedAt        time.Time
}
