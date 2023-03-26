//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import Cocoa
import Sentry
import Sparkle
import UserNotifications

class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var updaterController: SPUStandardUpdaterController?

    func applicationWillFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
    }

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        NSWindow.allowsAutomaticWindowTabbing = false

        if !AppConfig.c.debug {
            SentrySDK.start { options in
                options.dsn = "https://b72e32846ada4101bf63f27a1eeca89c@o120089.ingest.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
                options.appHangTimeoutInterval = 60 // 1 minute
            }
        }
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
