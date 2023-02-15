package sjwt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
)

const (
	NotBeforeLeeway = 24 * time.Hour
	NotAfterLeeway  = 12 * time.Hour

	drmVersion = 1
	appName    = appid.Codename
	// TODO
	shouldVerifyIdentifiers = false
)

var (
	ErrInvalidToken = errors.New("invalid token")
)

type jwtHeader struct {
	Type      string `json:"typ"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type jwtData struct {
	Header     *jwtHeader
	Claims     *drmtypes.JwtClaims
	Sig        []byte
	SigPayload []byte
}

type Verifier struct {
	clock             ClockSource
	pk                ed25519.PublicKey
	expectIdentifiers *drmtypes.Identifiers
	expectVersion     drmtypes.AppVersion
}

func NewVerifier(expectIdentifiers *drmtypes.Identifiers, expectVersion drmtypes.AppVersion) *Verifier {
	pk, err := parsePkBin(pkProdBin)
	if err != nil {
		panic(err)
	}

	return &Verifier{
		clock: currentClock(),
		pk:    pk,
	}
}

type TokenVerifyParams struct {
	StrictVersion bool
}

func (v *Verifier) Verify(token string, params TokenVerifyParams) (*drmtypes.ClaimInfo, error) {
	claims, err := decode(token, v.pk)
	if err != nil {
		return nil, err
	}

	// validate
	if claims.DrmVersion != drmVersion {
		return nil, ErrInvalidToken
	}
	if claims.AppName != appName {
		return nil, ErrInvalidToken
	}
	if shouldVerifyIdentifiers {
		if claims.DeviceID != v.expectIdentifiers.DeviceID {
			return nil, ErrInvalidToken
		}
		if claims.InstallID != v.expectIdentifiers.InstallID {
			return nil, ErrInvalidToken
		}
		if claims.ClientID != v.expectIdentifiers.ClientID {
			return nil, ErrInvalidToken
		}
	}
	if params.StrictVersion {
		if claims.AppVersion != v.expectVersion {
			// Version code is optional for startup / app upgrade
			return nil, ErrInvalidToken
		}
	}

	now := v.clock.Now()
	if /*wall*/ claims.NotBefore > now.Add(NotBeforeLeeway).Unix() {
		return nil, ErrInvalidToken
	}
	if /*wall*/ claims.ExpiresAt < now.Add(-NotAfterLeeway).Unix() {
		return nil, ErrInvalidToken
	}
	// Don't check issuedAt. notBefore is good enough of a constraint in case anything changes
	// in the future

	return &drmtypes.ClaimInfo{
		UserID:   claims.UserID,
		IssuedAt: time.Unix(claims.IssuedAt, 0).UTC(),

		ExpiresAt:     time.Unix(claims.ExpiresAt, 0).UTC(),
		LicenseEndsAt: time.Unix(claims.LicenseEndsAt, 0).UTC(),
		WarnAt:        time.Unix(claims.WarnAt, 0).UTC(),

		EntitlementTier: drmtypes.EntitlementTier(claims.EntitlementTier),
		EntitlementType: drmtypes.EntitlementType(claims.EntitlementType),
	}, nil
}

func parse(token string) (*jwtData, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}

	claimsData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}

	var header jwtHeader
	err = json.Unmarshal(headerData, &header)
	if err != nil {
		return nil, err
	}

	var claims drmtypes.JwtClaims
	err = json.Unmarshal(claimsData, &claims)
	if err != nil {
		return nil, err
	}

	return &jwtData{
		Header:     &header,
		Claims:     &claims,
		Sig:        signature,
		SigPayload: []byte(parts[0] + "." + parts[1]),
	}, nil
}

func decode(token string, pk ed25519.PublicKey) (*drmtypes.JwtClaims, error) {
	data, err := parse(token)
	if err != nil {
		return nil, err
	}

	// header
	if data.Header.Type != "JWT" {
		return nil, ErrInvalidToken
	}
	if data.Header.Algorithm != "EdDSA" {
		return nil, ErrInvalidToken
	}
	if data.Header.KeyID != "1" {
		return nil, ErrInvalidToken
	}

	// signature
	if !ed25519.Verify(pk, data.SigPayload, data.Sig) {
		return nil, ErrInvalidToken
	}

	// decode claims
	return data.Claims, nil
}
