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
    static let vmgrExe = "\(Folders.home)/code/projects/macvirt/macvmgr/OrbStack Helper (VM)"
    static let shellExe = "\(Folders.home)/code/projects/macvirt/macvmgr/bin/orb"
    static let dockerExe = "\(Folders.home)/code/projects/macvirt/macvmgr/xbin/docker"
    static let dockerComposeExe = "\(Folders.home)/code/projects/macvirt/macvmgr/xbin/docker-compose"
#else
    static let debug = false
    static let vmgrExe = pathForAuxiliaryExecutable("OrbStack Helper (VM)")
    static let shellExe = pathForAuxiliaryExecutable("bin/orb")
    static let dockerExe = pathForAuxiliaryExecutable("xbin/docker")
    static let dockerComposeExe = pathForAuxiliaryExecutable("xbin/docker-compose")
#endif
}

// can't crash because bundlePath can't be nil
private func pathForAuxiliaryExecutable(_ name: String) -> String {
    return "\(Bundle.main.bundlePath)/Contents/MacOS/\(name)"
}