//
// Created by Danny Lin on 4/1/23.
//

import Foundation
import SystemConfiguration
import Security

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

private func readProxySettings() -> ProxySettings {
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
            if let server = settings.httpProxy, let port = settings.httpPort {
                settings.httpPassword = getProxyPassword(proto: "htpx", server: server, port: port)
            }
        }

        let httpsEnable = dict["HTTPSEnable"] as? Bool ?? false
        if httpsEnable {
            settings.httpsEnable = true
            settings.httpsProxy = dict["HTTPSProxy"] as? String
            settings.httpsPort = dict["HTTPSPort"] as? Int
            settings.httpsUser = dict["HTTPSUser"] as? String
            if let server = settings.httpsProxy, let port = settings.httpsPort {
                settings.httpsPassword = getProxyPassword(proto: "htsx", server: server, port: port)
            }
        }

        let socksEnable = dict["SOCKSEnable"] as? Bool ?? false
        if socksEnable {
            settings.socksEnable = true
            settings.socksProxy = dict["SOCKSProxy"] as? String
            settings.socksPort = dict["SOCKSPort"] as? Int
            settings.socksUser = dict["SOCKSUser"] as? String
            if let server = settings.socksProxy, let port = settings.socksPort {
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
    if status == errSecSuccess {
        if let dict = result as? [String: Any] {
            if let data = dict[kSecValueData as String] as? Data {
                return String(data: data, encoding: .utf8)
            }
        }
    }

    return nil
}

@_cdecl("swext_proxy_get_settings")
func swext_proxy_get_settings() -> UnsafeMutablePointer<CChar> {
    let settings = readProxySettings()
    let data = try! JSONEncoder().encode(settings)
    let str = String(data: data, encoding: .utf8)!
    let cStr = UnsafeMutablePointer<CChar>(mutating: (str as NSString).utf8String!)
    // go frees the copy
    return strdup(cStr)
}
