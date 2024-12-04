//
//  main.swift
//  MacVirt
//
//  Created by Danny Lin on 12/4/24.
//

let subcommand = CommandLine.arguments.count >= 2 ? CommandLine.arguments[1] : ""

switch subcommand {
case "spawn-daemon":
    tcctrampMain()
default:
    MacVirtApp.main()
}
