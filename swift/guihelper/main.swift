//
//  main.swift
//  guihelper
//
//  Created by Danny Lin on 2/14/23.
//

import Foundation
import AppKit

func mainNotify(_ args: [String]) -> Int32 {
    let notification = NSUserNotification()
    notification.title = args[0]
    notification.informativeText = args[1]

    if args[2] != "" {
        notification.subtitle = args[2]
    }
    if args[3] == "--sound" {
        notification.soundName = NSUserNotificationDefaultSoundName
    }

    NSUserNotificationCenter.default.scheduleNotification(notification)
    RunLoop.current.run(until: Date.init(timeIntervalSinceNow: 0.1))
    return 0
}

let targetCmd = CommandLine.arguments[1]
let args = CommandLine.arguments.dropFirst(2).map { String($0) }
switch targetCmd {
case "notify":
    exit(mainNotify(args))
default:
    print("Unknown command")
    exit(1)
}
