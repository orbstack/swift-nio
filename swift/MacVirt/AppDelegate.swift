//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import AppKit
import Sentry
import Sparkle
import UserNotifications
import Defaults

class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var updaterController: SPUStandardUpdaterController?
    var actionTracker: ActionTracker!
    var windowTracker: WindowTracker!
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
                actionTracker: actionTracker, windowTracker: windowTracker,
                vmModel: vmModel)
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if sender.activationPolicy() == .accessory {
            // we're currently in menu bar mode.
            // is VM running?
            if vmModel.state == .stopped {
                // already fully stopped, so safe to terminate now
                return .terminateNow
            } else {
                // VM is running, so do a graceful shutdown and terminate later
                Task { @MainActor in
                    // we don't care if this fails or succeeds, just give it best effort and terminate when done
                    NSLog("preparing to terminate")
                    await vmModel.tryStop()
                    NSLog("terminating")
                    menuBar.hide()
                    sender.reply(toApplicationShouldTerminate: true)

                    // if it's not working, just terminate now
                    // could happen if user keeps menu open when we reply to terminate
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        NSLog("force terminating")
                        sender.terminate(nil)
                    }
                }

                return .terminateLater
            }
        } else {
            // we're not in menu bar mode.
            // is menu enabled?
            if Defaults[.globalShowMenubarExtra] {
                // enter menu bar mode by closing all windows, then cancel termination
                for window in NSApp.windows {
                    if window.isUserFacing {
                        window.close()
                    }
                }
                return .terminateCancel
            } else {
                // menu bar is disabled
                // TODO show warning modal and terminate later if user accepts
                // TODO careful about open windows. e.g. if user only has logs open?
                return .terminateNow
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
