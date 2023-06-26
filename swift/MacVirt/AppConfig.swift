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
#if DEBUG
    static let debug = true
    static let vmgrExe = "\(Folders.home)/code/projects/macvirt/vmgr/OrbStack Helper (VM)"
#else
    static let debug = false
    static let vmgrExe = pathForAuxiliaryExecutable("OrbStack Helper (VM)")
#endif
    static let shellExe = pathForAuxiliaryExecutable("bin/orb")
    static let dockerExe = pathForAuxiliaryExecutable("xbin/docker")
    static let dockerComposeExe = pathForAuxiliaryExecutable("xbin/docker-compose")
}

// can't crash because bundlePath can't be nil
private func pathForAuxiliaryExecutable(_ name: String) -> String {
    return "\(Bundle.main.bundlePath)/Contents/MacOS/\(name)"
}
