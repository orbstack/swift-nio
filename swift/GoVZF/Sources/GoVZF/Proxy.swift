//
// Created by Danny Lin on 4/1/23.
//

import Foundation
import SystemConfiguration
import Security
import CBridge

struct ProxySettings: Codable {
    var httpEnable: Bool
    var httpProxy: String?
    var httpPort: Int?
    var httpUser: String?
    var httpPassword: String?

    var httpsEnable: Bool
    var httpsProxy: String?
    var httpsPort: Int?
    var httpsUser: String?
    var httpsPassword: String?

    var socksEnable: Bool
    var socksProxy: String?
    var socksPort: Int?
    var socksUser: String?
    var socksPassword: String?

    var exceptionsList: [String]
}

private func readProxySettings(needAuth: Bool) -> ProxySettings {
    var settings = ProxySettings(
        httpEnable: false,
        httpProxy: nil,
        httpPort: nil,
        httpsEnable: false,
        httpsProxy: nil,
        httpsPort: nil,
        socksEnable: false,
        socksProxy: nil,
        socksPort: nil,
        exceptionsList: []
    )

    if let dict = SCDynamicStoreCopyProxies(nil) as? [String: Any] {
        let httpEnable = dict["HTTPEnable"] as? Bool ?? false
        if httpEnable {
            settings.httpEnable = true
            settings.httpProxy = dict["HTTPProxy"] as? String
            settings.httpPort = dict["HTTPPort"] as? Int
            settings.httpUser = dict["HTTPUser"] as? String
            if needAuth, let server = settings.httpProxy, let port = settings.httpPort {
                settings.httpPassword = getProxyPassword(proto: "htpx", server: server, port: port)
            }
        }

        let httpsEnable = dict["HTTPSEnable"] as? Bool ?? false
        if httpsEnable {
            settings.httpsEnable = true
            settings.httpsProxy = dict["HTTPSProxy"] as? String
            settings.httpsPort = dict["HTTPSPort"] as? Int
            settings.httpsUser = dict["HTTPSUser"] as? String
            if needAuth, let server = settings.httpsProxy, let port = settings.httpsPort {
                settings.httpsPassword = getProxyPassword(proto: "htsx", server: server, port: port)
            }
        }

        let socksEnable = dict["SOCKSEnable"] as? Bool ?? false
        if socksEnable {
            settings.socksEnable = true
            settings.socksProxy = dict["SOCKSProxy"] as? String
            settings.socksPort = dict["SOCKSPort"] as? Int
            settings.socksUser = dict["SOCKSUser"] as? String
            if needAuth, let server = settings.socksProxy, let port = settings.socksPort {
                settings.socksPassword = getProxyPassword(proto: "sox ", server: server, port: port)
            }
        }

        settings.exceptionsList = dict["ExceptionsList"] as? [String] ?? []
    }

    return settings
}

// https://chromium.googlesource.com/external/webrtc/+/6acd9f49d9b3/webrtc/base/proxydetect.cc
private func getProxyPassword(proto: String, server: String, port: Int) -> String? {
    let query: [String: Any] = [
        kSecClass as String: kSecClassInternetPassword,
        kSecAttrServer as String: server,
        kSecAttrPort as String: port,
        kSecAttrProtocol as String: proto,
        kSecReturnAttributes as String: true,
        kSecReturnData as String: true,
    ]

    var result: AnyObject?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    if status == errSecSuccess,
       let dict = result as? [String: Any],
       let data = dict[kSecValueData as String] as? Data {
        return String(data: data, encoding: .utf8)
    }

    return nil
}

enum SwextError: Error {
    case fetchCertificate(status: OSStatus)
}

private func checkSslTrustSettings(_ certificate: SecCertificate, domain: SecTrustSettingsDomain) -> Bool {
    var results: CFArray?
    let status = SecTrustSettingsCopyTrustSettings(certificate, domain, &results)
    guard status == errSecSuccess, let results else {
        return false
    }

    for trustSettings in results as! [[String: Any]] {
        let policy = trustSettings[kSecTrustSettingsPolicy] as! SecPolicy
        let props = SecPolicyCopyProperties(policy) as! [String: Any]
        // make sure this policy is for SSL, otherwise it's not relevant to us
        guard let policy = trustSettings[kSecTrustSettingsPolicy] as! SecPolicy?,
              let props = SecPolicyCopyProperties(policy) as? [String: Any],
              props[kSecPolicyOid as String] as? String == (kSecPolicyAppleSSL as String) else {
            continue
        }

        // and make sure it's trusted
        // this doubles as a root cert check, b/c .trustRoot == root, and .trustAsRoot == not root
        // (we're technically supposed to check whether cert is root, and assume kSecTrustSettingsResult==trustRoot default)
        if let result = trustSettings[kSecTrustSettingsResult] as? Int,
           result == SecTrustSettingsResult.trustRoot.rawValue {
            return true
        }
    }

    return false
}

private func getExtraCaCerts(filterRootOnly: Bool = true) throws -> [String] {
    let query: [String: Any] = [
        kSecClass as String: kSecClassCertificate,
        kSecMatchLimit as String: kSecMatchLimitAll,
        kSecReturnRef as String: true,
        kSecAttrCanVerify as String: true,
    ]

    var result: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    guard status == errSecSuccess else {
        throw SwextError.fetchCertificate(status: status)
    }

    let certs = result as! [SecCertificate]
    let extraCaCerts = certs.filter { certificate in
        // check both user and admin trust settings
        // (system is read-only roots)
        checkSslTrustSettings(certificate, domain: .user) ||
                checkSslTrustSettings(certificate, domain: .admin)
    }

    return extraCaCerts.compactMap { certificate in
        guard let data = SecCertificateCopyData(certificate) as Data? else {
            return nil
        }

        let base64EncodedData = data.base64EncodedString(options: .lineLength64Characters)
        return """
               -----BEGIN CERTIFICATE-----
               \(base64EncodedData)
               -----END CERTIFICATE-----

               """
    }
}

@_cdecl("swext_proxy_get_settings")
func swext_proxy_get_settings(needAuth: Bool) -> UnsafeMutablePointer<CChar> {
    let settings = readProxySettings(needAuth: needAuth)
    let data = try! JSONEncoder().encode(settings)
    let str = String(data: data, encoding: .utf8)!
    // go frees the copy
    return strdup(str)
}

private let scSessionName = "dev.orbstack.swext.sc"
private let scKeyProxies = "State:/Network/Global/Proxies"

@_cdecl("swext_proxy_monitor_changes")
func swext_proxy_monitor_changes() -> GResultErr {
    func callback(store: SCDynamicStore, changedKeys: CFArray, info: UnsafeMutableRawPointer?) {
        let keys = changedKeys as! [String]
        if keys.contains(scKeyProxies) {
            swext_proxy_cb_changed()
        }
    }

    let store = SCDynamicStoreCreate(nil, scSessionName as CFString, callback, nil)!
    guard SCDynamicStoreSetNotificationKeys(store, [scKeyProxies] as CFArray, nil) else {
        return GResultErr(err: strdup("failed to set notification keys"))
    }
    let source = SCDynamicStoreCreateRunLoopSource(nil, store, 0)
    CFRunLoopAddSource(CFRunLoopGetCurrent(), source, .defaultMode)

    // return but retain store
    let _ = Unmanaged.passRetained(store)
    return GResultErr(err: nil)
}

@_cdecl("swext_security_get_extra_ca_certs")
func swext_security_get_extra_ca_certs() -> UnsafeMutablePointer<CChar> {
    do {
        let certs = try getExtraCaCerts()
        let data = try JSONEncoder().encode(certs)
        let str = String(data: data, encoding: .utf8)!
        // go frees the copy
        return strdup(str)
    } catch {
        return strdup("E\(error)")
    }
}
