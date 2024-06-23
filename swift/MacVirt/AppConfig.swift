//
//  AppConfig.swift
//  MacVirt
//
//  Created by Danny Lin on 2/4/23.
//

import Foundation

enum Constants {
    static let userAppName = "OrbStack"
}

enum AppConfig {
    static let vmgrExeName = "OrbStack Helper"
    #if DEBUG
        static let debug = true
        // TODO: dedupe and fix ext-swift version
        static let vmgrExe = "\(Bundle.main.bundlePath)/Contents/Frameworks/\(vmgrExeName).app/Contents/MacOS/\(vmgrExeName)"
        static let apiBaseUrl = "http://localhost:8400"
    #else
        static let debug = false
        // must launch from bundle path. symlink causes macOS to use our app bundle ID for NSRunningApplication instead
        static let vmgrExe = "\(Bundle.main.bundlePath)/Contents/Frameworks/\(vmgrExeName).app/Contents/MacOS/\(vmgrExeName)"
        static let apiBaseUrl = "https://api-misc.orbstack.dev"
    #endif
    static let shellExe = pathForAuxiliaryExecutable("bin/orb")
    static let ctlExe = pathForAuxiliaryExecutable("bin/orbctl")
    static let dockerExe = pathForAuxiliaryExecutable("xbin/docker")
    static let dockerComposeExe = pathForAuxiliaryExecutable("xbin/docker-compose")
    static let kubectlExe = pathForAuxiliaryExecutable("xbin/kubectl")

    #if arch(arm64)
    static let nativeArchs = Set(["arm64"])
    #else
    static let nativeArchs = Set(["amd64", "386"])
    #endif
}

// can't crash because bundlePath can't be nil
private func pathForAuxiliaryExecutable(_ name: String) -> String {
    return "\(Bundle.main.bundlePath)/Contents/MacOS/\(name)"
}
