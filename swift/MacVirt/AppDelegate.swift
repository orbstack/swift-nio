//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import Cocoa
import Sentry

class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ aNotification: Notification) {
        NSWindow.allowsAutomaticWindowTabbing = false

        if !AppConfig.c.debug {
            SentrySDK.start { options in
                options.dsn = "https://e97d84d2f3ad48dbac3c491be1ad4c52@o120089.ingest.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
            }
        }
    }

    func applicationWillTerminate(_ aNotification: Notification) {
        // Insert code here to tear down your application
    }
}
