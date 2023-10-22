//
// Created by Danny Lin on 9/18/23.
//

import Foundation
import Security
import CBridge

private let dummyPtr = Unmanaged.passRetained(NSObject()).toOpaque()

private enum KeychainFFIError: Error {
    case certificateInvalid
    case addToKeychainFailed(OSStatus)
    case setTrustSettingsFailed(OSStatus)
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

    static func importAndTrustCertificate(certDer: Data) throws {
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
                    return
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
    }
}

@_cdecl("swext_security_import_certificate")
func swext_security_import_certificate(certDerB64C: UnsafePointer<CChar>) -> GResultErr {
    let certDerB64 = String(cString: certDerB64C)
    let certDer = Data(base64Encoded: certDerB64)!

    // need a dummy pointer to use wrapper
    return doGenericErr(dummyPtr) { (_: PHClient) in
        try Keychain.importAndTrustCertificate(certDer: certDer)
    }
}