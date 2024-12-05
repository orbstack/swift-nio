//
//  set-launch-at-login.swift
//  MacVirt
//
//  Created by Danny Lin on 12/5/24.
//

import LaunchAtLogin

func setLaunchAtLoginMain() {
    LaunchAtLogin.isEnabled = (CommandLine.arguments[2] == "true")
}
