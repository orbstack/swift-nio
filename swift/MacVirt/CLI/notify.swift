//
//  notify.swift
//  MacVirt
//
//  Created by Danny Lin on 12/6/24.
//

import Foundation
import UserNotifications

func notifyMain() -> Int32 {
    let args = CommandLine.arguments.dropFirst(2).map { String($0) }

    let content = UNMutableNotificationContent()
    content.title = args[0]
    content.body = args[1]

    if args[2] != "" {
        content.subtitle = args[2]
    }
    if args[3] == "--sound" {
        content.sound = UNNotificationSound.default
    }
    if args[4] != "" {
        content.userInfo = ["url": args[4]]
    }

    let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 0.001, repeats: false)
    let request = UNNotificationRequest(
        identifier: UUID().uuidString, content: content, trigger: trigger)
    let center = UNUserNotificationCenter.current()
    center.add(request) { error in
        if let error {
            print("Failed to post notification: \(error)")
            exit(1)
        }
    }
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.1))
    return 0
}
