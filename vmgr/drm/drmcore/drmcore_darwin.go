//go:build darwin

package drmcore

import (
	"encoding/json"
	"fmt"

	"github.com/keybase/go-keychain"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
)

const (
	// avoid perm issues bug for diff bundle IDs
	keychainService = appid.BundleID // user-facing "Where"
	// bumped when migrating to access group
	keychainAccount     = "license_state2"
	keychainAccountOld  = "license_state"
	keychainLabel       = "OrbStack" // user-facing "Name"
	keychainAccessGroup = "HUAQ24HBR6.dev.orbstack"
)

func SaveRefreshToken(refreshToken string) error {
	data, err := keychain.GetGenericPassword(keychainService, keychainAccount, keychainLabel, keychainAccessGroup)
	if err != nil {
		// fail is ok, just start fresh
	}

	var state drmtypes.PersistentState
	if len(data) > 0 {
		_ = json.Unmarshal(data, &state)
		// if it's invalid, discard it and continue. we still want to save a token
	}

	state.RefreshToken = refreshToken

	data, err = json.Marshal(&state)
	if err != nil {
		return err
	}

	err = SetKeychainState(data)
	if err != nil {
		return fmt.Errorf("set keychain: %w", err)
	}

	return nil
}

func ReadKeychainState() ([]byte, error) {
	data, err := keychain.GetGenericPassword(keychainService, keychainAccount, keychainLabel, keychainAccessGroup)
	if err != nil {
		// retry w/ old, for seamless migration
		// next SetKeychainState call should move it
		data, err = keychain.GetGenericPassword(keychainService, keychainAccountOld, keychainLabel, keychainAccessGroup)
		if err != nil {
			// use new
			return nil, err
		}
	}

	return data, nil
}

func SetKeychainState(data []byte) error {
	// delete old if necessary
	// update is too complicated
	// also helps fix permissinos in case signing ID changed
	deleteErr := keychain.DeleteGenericPasswordItem(keychainService, keychainAccount)

	// also delete pre-migration if necessary
	_ = keychain.DeleteGenericPasswordItem(keychainService, keychainAccountOld)

	item := keychain.NewGenericPassword(keychainService, keychainAccount, keychainLabel, data, keychainAccessGroup)
	// enable dataProtection for iOS permissions mode - otherwise access group doesn't work
	item.SetDataProtection()
	item.SetSynchronizable(keychain.SynchronizableNo) // tokens are tied to device
	item.SetAccessible(keychain.AccessibleAlways)     // for headless usage
	err := keychain.AddItem(item)
	if err != nil {
		return fmt.Errorf("%w (delete: %w)", err, deleteErr)
	}

	return nil
}
