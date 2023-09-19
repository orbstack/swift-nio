package drmtypes

import "github.com/orbstack/macvirt/vmgr/drm/timex"

// saved as generic app password in keychain
type PersistentState struct {
	RefreshToken     string              `json:"refresh_token,omitempty"`
	EntitlementToken string              `json:"entitlement_token,omitempty"`
	FetchedAt        timex.MonoSleepTime `json:"fetched_at,omitempty"`
}

type AppDrmMeta struct {
	
}
