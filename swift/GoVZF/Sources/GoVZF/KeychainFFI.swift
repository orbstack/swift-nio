//
// Created by Danny Lin on 9/18/23.
//

import Foundation
import Security
import CBridge
import AppKit
import Defaults

// must match maxCertDismissCount in Go (scon/agent)
// after 2 dismissals, we auto-disable the HTTPS config
private let maxCertDismissCount = 2

private let firefoxRecentPeriod: TimeInterval = 3 * 30 * 24 * 60 * 60
private let firefoxBundleIds = [
    // stable / beta
    "org.mozilla.firefox",
    // nightly
    "org.mozilla.nightly",
    // developer edition
    "org.mozilla.firefoxdeveloperedition",
]

private func isFirefoxRecentlyUsed() -> Bool {
    return firefoxBundleIds.contains { bundleId in
        if let bundleUrl = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleId),
           let attributes = NSMetadataItem(url: bundleUrl),
           let date = attributes.value(forAttribute: kMDItemLastUsedDate as String) as? Date,
           date.timeIntervalSinceNow < 365 * 24 * 60 * 60 {
            return true
        } else {
            return false
        }
    }
}

private func importFirefoxCerts() {
    // conds: firefox last used within a year
    // this avoids triggering suspicious filesystem accesses (if we check profile dates)
    if isFirefoxRecentlyUsed() {
        // open docs page
        NSWorkspace.shared.open(URL(string: "https://go.orbstack.dev/firefox-cert")!)
    }
}

private enum KeychainFFIError: Error {
    case certificateInvalid
    case addToKeychainFailed(OSStatus)
    case setTrustSettingsFailed(OSStatus)

    case tooManyDeclines(Error)
}

private struct Keychain {
    private static let service = Bundle.main.bundleIdentifier!
    private static let accountDrm = "license_state2"
    private static let label = "OrbStack"
    private static let accessGroup = "HUAQ24HBR6.dev.orbstack"

    private static func deleteGenericItem(service: String, account: String) {
        SecItemDelete([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
        ] as [CFString: Any] as CFDictionary)
    }

    static func deleteToken() {
        deleteGenericItem(service: service, account: accountDrm)
    }

    static func setToken(_ token: String) {
        deleteToken()

        SecItemAdd([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: accountDrm,
            kSecValueData: token.data(using: .utf8)!,
            kSecAttrLabel: label,
            kSecAttrAccessGroup: accessGroup,
        ] as [CFString: Any] as CFDictionary, nil)
    }

    static func getToken() -> String? {
        var result: CFTypeRef?
        let status = SecItemCopyMatching([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: accountDrm,
            kSecReturnData: true,
            kSecAttrAccessGroup: accessGroup,
        ] as [CFString: Any] as CFDictionary, &result)
        guard status == errSecSuccess else {
            return nil
        }
        guard let data = result as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    static func importAndTrustCertificate(certDer: Data) throws -> Bool {
        let cert = SecCertificateCreateWithData(nil, certDer as CFData)
        guard let cert else {
            throw KeychainFFIError.certificateInvalid
        }

        var status = SecCertificateAddToKeychain(cert, nil)
        guard status == errSecSuccess || status == errSecDuplicateItem else {
            throw KeychainFFIError.addToKeychainFailed(status)
        }

        // if duplicate, skip trust settings update (to avoid auth prompt) by validating for SSL
        if status == errSecDuplicateItem {
            var trust: SecTrust?
            let policy = SecPolicyCreateSSL(true, nil)
            SecTrustCreateWithCertificates(cert, policy, &trust)
            if let trust {
                // disable network fetch - can't block here
                SecTrustSetNetworkFetchAllowed(trust, false)

                var error: CFError?
                let result = SecTrustEvaluateWithError(trust, &error)
                if result && error == nil {
                    return false
                }
            }
        }

        // mark as trusted but only for SSL and X509 basic policy
        status = SecTrustSettingsSetTrustSettings(cert, .user, [
            [
                kSecTrustSettingsResult: NSNumber(value: SecTrustSettingsResult.trustRoot.rawValue),
                kSecTrustSettingsPolicy: SecPolicyCreateSSL(true, nil),
            ] as [String: Any] as CFDictionary,
            [
                kSecTrustSettingsResult: NSNumber(value: SecTrustSettingsResult.trustRoot.rawValue),
                kSecTrustSettingsPolicy: SecPolicyCreateBasicX509(),
            ] as [String: Any] as CFDictionary,
        ] as CFArray)
        guard status == errSecSuccess else {
            throw KeychainFFIError.setTrustSettingsFailed(status)
        }

        return true
    }
}

@_cdecl("swext_security_import_certificate")
func swext_security_import_certificate(certDerB64C: UnsafePointer<CChar>) -> GResultErr {
    let certDerB64 = String(cString: certDerB64C)
    let certDer = Data(base64Encoded: certDerB64)!

    return doGenericErr {
        do {
            let imported = try Keychain.importAndTrustCertificate(certDer: certDer)
            if imported {
                importFirefoxCerts()
            }
        } catch {
            // consider every error
            // not really true but in practice it's the same
            Defaults[.networkHttpsDismissCount] += 1
            if Defaults[.networkHttpsDismissCount] >= maxCertDismissCount {
                throw KeychainFFIError.tooManyDeclines(error)
            }

            throw error
        }
    }
}