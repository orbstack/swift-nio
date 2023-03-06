//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import Cocoa
import Sentry
import Sparkle

class AppDelegate: NSObject, NSApplicationDelegate {
    var updaterController: SPUStandardUpdaterController?

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        NSWindow.allowsAutomaticWindowTabbing = false

        if !AppConfig.c.debug {
            SentrySDK.start { options in
                options.dsn = "https://e115a1e5bb7a453f93fada4fadc4c3ac@o120089.ingest.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
            }
        }
    }

    func applicationWillTerminate(_ aNotification: Notification) {
        // Insert code here to tear down your application
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls {
            // CLI, to trigger GUI to update
            // macvirt://update
            if url.scheme == "macvirt" && url.host == "update" {
                updaterController?.updater.checkForUpdates()
            }
        }
    }
}
