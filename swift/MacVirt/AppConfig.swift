//
//  AppConfig.swift
//  MacVirt
//
//  Created by Danny Lin on 2/4/23.
//

import Foundation

class AppConfig {
#if DEBUG
    static let c = AppConfig(
        debug: true,
        vmgrExePath: "/Users/dragon/code/projects/macvirt/macvmgr/macvmgr"
    )
#else
    static let c = AppConfig(
        debug: false,
        vmgrExePath: nil
    )
#endif
    
    let debug: Bool
    let vmgrExePath: String?
    
    init(debug: Bool, vmgrExePath: String?) {
        self.debug = debug
        self.vmgrExePath = vmgrExePath
    }
}

