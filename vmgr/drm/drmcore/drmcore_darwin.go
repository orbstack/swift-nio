//go:build darwin

// TODO: these are almost all keychain functions. should be a kcutil package instead

package drmcore

import (
	"encoding/json"
	"fmt"

	"github.com/keybase/go-keychain"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
)

const (
	// avoid perm issues bug for diff bundle IDs
	keychainService     = appid.BundleID // user-facing "Where"
	keychainAccessGroup = "HUAQ24HBR6.dev.orbstack"

	// bumped when migrating to access group
	keychainAccountDrm    = "license_state2"
	keychainAccountDrmOld = "license_state"
	keychainLabelDrm      = "OrbStack account" // user-facing "Name"
	keychainLabelDrmOld   = "OrbStack"         // user-facing "Name"

	// for TLS proxy
	keychainLabelTLS   = "OrbStack CA data"
	keychainAccountTLS = "tls_ca_data"
)

func SaveRefreshToken(refreshToken string) error {
	// err is ok, just start fresh
	data, _ := ReadKeychainDrmState()

	var state drmtypes.PersistentState
	if len(data) > 0 {
		_ = json.Unmarshal(data, &state)
		// if it's invalid, discard it and continue. we still want to save a token
	}

	state.RefreshToken = refreshToken

	data, err := json.Marshal(&state)
	if err != nil {
		return err
	}

	err = SetKeychainDrmState(data)
	if err != nil {
		return fmt.Errorf("set keychain: %w", err)
	}

	return nil
}

func ReadKeychainDrmState() ([]byte, error) {
	data, err := readGenericPassword(keychainAccountDrm, keychainLabelDrm)
	if err != nil {
		// retry w/ old, for seamless migration
		// next SetKeychainState call should move it
		data, err = readGenericPassword(keychainAccountDrmOld, keychainLabelDrmOld)
		if err != nil {
			// use new
			return nil, err
		}
	}

	return data, nil
}

func readGenericPassword(account, label string) ([]byte, error) {
	return keychain.GetGenericPassword(keychainService, account, label, keychainAccessGroup)
}

func setGenericPassword(account, label string, data []byte) error {
	// delete old if necessary
	// update is too complicated
	// also helps fix permissinos in case signing ID changed
	deleteErr := keychain.DeleteGenericPasswordItem(keychainService, account)

	item := keychain.NewGenericPassword(keychainService, account, label, data, keychainAccessGroup)
	// enable dataProtection for iOS permissions mode - otherwise access group doesn't work
	item.SetDataProtection()
	// DRM tokens: tied to device ID
	// TLS: certificate won't be installed on new device if synced
	item.SetSynchronizable(keychain.SynchronizableNo)
	// allow headless usage
	item.SetAccessible(keychain.AccessibleAlways)
	err := keychain.AddItem(item)
	if err != nil {
		return fmt.Errorf("%w (delete: %w)", err, deleteErr)
	}

	return nil
}

func SetKeychainDrmState(data []byte) error {
	// delete pre-migration if necessary
	_ = keychain.DeleteGenericPasswordItem(keychainService, keychainAccountDrmOld)

	err := setGenericPassword(keychainAccountDrm, keychainLabelDrm, data)
	if err != nil {
		return err
	}

	return nil
}

func ReadKeychainTLSData() (*htypes.KeychainTLSData, error) {
	data, err := readGenericPassword(keychainAccountTLS, keychainLabelTLS)
	if err != nil {
		return nil, err
	}

	// if empty, that means we don't have a cert
	if len(data) == 0 {
		return nil, nil
	}

	var tlsData htypes.KeychainTLSData
	err = json.Unmarshal(data, &tlsData)
	if err != nil {
		return nil, err
	}

	return &tlsData, nil
}

func SetKeychainTLSData(tlsData *htypes.KeychainTLSData) error {
	data, err := json.Marshal(tlsData)
	if err != nil {
		return err
	}

	err = setGenericPassword(keychainAccountTLS, keychainLabelTLS, data)
	if err != nil {
		return err
	}

	return nil
}
