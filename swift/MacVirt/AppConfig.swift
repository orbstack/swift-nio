//
//  AppConfig.swift
//  MacVirt
//
//  Created by Danny Lin on 2/4/23.
//

import Foundation

struct Constants {
    static let userAppName = "OrbStack"
}

struct AppConfig {
    static let vmgrExeName = "OrbStack Helper (VM)"
#if DEBUG
    static let debug = true
    static let vmgrExe = "\(Folders.home)/code/projects/macvirt/out/\(vmgrExeName).app/Contents/MacOS/\(vmgrExeName)"
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
}

// can't crash because bundlePath can't be nil
private func pathForAuxiliaryExecutable(_ name: String) -> String {
    return "\(Bundle.main.bundlePath)/Contents/MacOS/\(name)"
}
