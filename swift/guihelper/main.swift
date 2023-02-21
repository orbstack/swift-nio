//
//  main.swift
//  guihelper
//
//  Created by Danny Lin on 2/14/23.
//

import Foundation
import UserNotifications

func mainNotify(_ args: [String]) -> Int32 {
    let content = UNMutableNotificationContent()
    content.title = args[0]
    content.body = args[1]

    if args[2] != "" {
        content.subtitle = args[2]
    }
    if args[3] == "--sound" {
        content.sound = UNNotificationSound.default
    }

    let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 0.001, repeats: false)
    let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: trigger)
    let center = UNUserNotificationCenter.current()
    center.add(request) { (error) in
        if let error = error {
            print("Failed to post notification: \(error)")
        }
    }
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.1))
    return 0
}

func mainRunAdmin(_ args: [String]) -> Int32 {
    let script = args[0]
    let prompt = args[1]
    do {
        try runAsAdmin(script: script, prompt: prompt)
    } catch {
        print("Failed to run as admin: \(error)")
        return 1
    }
    return 0
}

let targetCmd = CommandLine.arguments[1]
let args = CommandLine.arguments.dropFirst(2).map { String($0) }
switch targetCmd {
case "notify":
    exit(mainNotify(args))
case "run-admin":
    exit(mainRunAdmin(args))
default:
    print("Unknown command")
    exit(1)
}
