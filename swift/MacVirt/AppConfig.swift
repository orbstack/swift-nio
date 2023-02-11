//
//  AppConfig.swift
//  MacVirt
//
//  Created by Danny Lin on 2/4/23.
//

import Foundation

struct Constants {
    static let userAppName = "MoonStack"
}

struct AppConfig {
#if DEBUG
    static let c = AppConfig(
        debug: true,
        vmgrExe: "/Users/dragon/code/projects/macvirt/macvmgr/macvmgr",
        shellExe: "/Users/dragon/code/projects/macvirt/macvmgr/bin/moon",
        dockerExe: "/Users/dragon/code/projects/macvirt/macvmgr/xbin/docker"
    )
#else
    static let c = AppConfig(
        debug: false,
        vmgrExe: Bundle.main.path(forAuxiliaryExecutable: "macvmgr")!,
        shellExe: Bundle.main.path(forAuxiliaryExecutable: "bin/moon")!,
        dockerExe: Bundle.main.path(forAuxiliaryExecutable: "xbin/docker")!
    )
#endif
    
    let debug: Bool
    let vmgrExe: String
    let shellExe: String
    let dockerExe: String
}

