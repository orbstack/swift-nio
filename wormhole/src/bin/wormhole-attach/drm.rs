use std::collections::HashSet;

use anyhow::bail;
use jwt_simple::{
    algorithms::{Ed25519PublicKey, EdDSAPublicKeyLike},
    common::VerificationOptions,
    reexports::coarsetime::Duration,
};
use serde::{Deserialize, Serialize};

// larger of Go sjwt's leeway (NotBeforeLeeway)
const NOT_BEFORE_LEEWAY: u64 = 12 * 60 * 60; // 12 hours

// params matching sjwt
const DRM_VERSION: u32 = 1;
const APP_NAME: &str = "macvirt";

#[derive(Serialize, Deserialize)]
struct AppVersion {
    code: u32,
    git: String,
}

#[derive(Serialize, Deserialize)]
enum EntitlementTier {
    None = 0,
    Pro = 1,
    Reserved = 2,
    Enterprise = 3,
}

// to minimize risk of future breakage, don't decode anything we don't use
#[derive(Serialize, Deserialize)]
struct DrmClaims {
    /*
    #[serde(rename = "sub")]
    user_id: String,
    */
    #[serde(rename = "ent")]
    entitlement_tier: i32,
    /*
    #[serde(rename = "etp")]
    entitlement_type: EntitlementType,
    #[serde(rename = "emg")]
    entitlement_message: Option<String>,
    #[serde(rename = "est")]
    entitlement_status: Option<EntitlementStatus>,
    */

    // standard: aud
    /*
    #[serde(rename = "ver")]
    app_version: AppVersion,

    #[serde(rename = "did")]
    device_id: String,
    #[serde(rename = "iid")]
    install_id: String,
    #[serde(rename = "cid")]
    client_id: String,
    */
    // standard: iss

    // standard: iat, exp, nbf
    #[serde(rename = "dvr")]
    drm_version: u32,
    /*
    #[serde(rename = "war")]
    warn_at: i64,
    #[serde(rename = "lxp")]
    license_ends_at: i64,
    */
}

pub fn verify_token(token: &str) -> anyhow::Result<()> {
    let pk_bytes = include_bytes!("../../../jwt-prod.pub.bin");
    let pk = Ed25519PublicKey::from_bytes(pk_bytes)?;

    let mut options = VerificationOptions::default();
    options.time_tolerance = Some(Duration::from_secs(NOT_BEFORE_LEEWAY));
    options.allowed_audiences = Some(HashSet::from([APP_NAME.to_string()]));

    let claims = pk.verify_token::<DrmClaims>(token, Some(options))?;

    if claims.custom.drm_version != DRM_VERSION {
        bail!("invalid drm version");
    }

    if claims.custom.entitlement_tier == 0 {
        bail!("invalid entitlement tier");
    }

    Ok(())
}
