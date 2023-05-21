//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import AppKit
import Sentry
import Sparkle
import UserNotifications

class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var updaterController: SPUStandardUpdaterController?
    var vmModel: VmViewModel!

    private var menuBar: MenuBarController!

    func applicationWillFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
        //NSApp.setActivationPolicy(.accessory)
    }

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        NSWindow.allowsAutomaticWindowTabbing = false

        if !AppConfig.debug {
            SentrySDK.start { options in
                options.dsn = "https://8e78517a949a4070a56b23fc1f7b8184@o120089.ingest.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
                options.appHangTimeoutInterval = 60 // 1 minute
            }
        }

        // Menu bar status item
        menuBar = MenuBarController(updaterController: updaterController!,
                vmModel: vmModel)
    }

    func applicationWillTerminate(_ aNotification: Notification) {
        // Insert code here to tear down your application
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls {
            // CLI, to trigger GUI to update
            // orbstack://update
            if url.scheme == "orbstack" && url.host == "update" {
                updaterController?.updater.checkForUpdates()
            }
        }
    }

    // notification
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        if let url = response.notification.request.content.userInfo["url"] as? String {
            NSWorkspace.shared.open(URL(string: url)!)
        }
    }
}
