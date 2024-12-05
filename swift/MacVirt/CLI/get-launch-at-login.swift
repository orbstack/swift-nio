//
//  get-launch-at-login.swift
//  MacVirt
//
//  Created by Danny Lin on 12/5/24.
//

import LaunchAtLogin

func getLaunchAtLoginMain() {
    if LaunchAtLogin.isEnabled {
        print("true")
    } else {
        print("false")
    }
}
