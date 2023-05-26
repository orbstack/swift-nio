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
    static let vmgrExe = Bundle.main.path(forAuxiliaryExecutable: "OrbStack Helper (VM)")!
    static let shellExe = Bundle.main.path(forAuxiliaryExecutable: "bin/orb")!
    static let dockerExe = Bundle.main.path(forAuxiliaryExecutable: "xbin/docker")!
    static let dockerComposeExe = Bundle.main.path(forAuxiliaryExecutable: "xbin/docker-compose")!
#endif
}
