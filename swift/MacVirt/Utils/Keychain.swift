//
// Created by Danny Lin on 9/18/23.
//

import Foundation
import Security

struct Keychain {
    private static let service = Bundle.main.bundleIdentifier!
    private static let account = "license_state2"
    private static let label = "OrbStack"
    private static let accessGroup = "HUAQ24HBR6.dev.orbstack"

    private static func deleteGenericItem(service: String, account: String) {
        SecItemDelete([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
        ] as [CFString: Any] as CFDictionary)
        SecTrustSettingsSetTrustSettings(<#T##certRef: SecCertificate##Security.SecCertificate#>, <#T##domain: SecTrustSettingsDomain##Security.SecTrustSettingsDomain#>, <#T##trustSettingsDictOrArray: CFTypeRef?##CoreFoundation.CFTypeRef?#>)
    }

    static func deleteToken() {
        deleteGenericItem(service: service, account: account)
    }

    static func setToken(_ token: String) {
        deleteToken()

        SecItemAdd([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
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
            kSecAttrAccount: account,
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
}