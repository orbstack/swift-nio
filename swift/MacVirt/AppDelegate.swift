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
        if sender.activationPolicy() == .accessory || !Defaults[.globalShowMenubarExtra] {
            // we're currently in menu bar mode, or menu bar is disabled.
            // in both cases, we stop VM and then terminate

            // is VM running?
            if vmModel.state == .stopped {
                // already fully stopped, so safe to terminate now
                return .terminateNow
            } else {
                // VM is running, so do a graceful shutdown and terminate later
                func finishTerminate() {
                    menuBar.hide()
                    sender.reply(toApplicationShouldTerminate: true)

                    // if it's not working, just terminate now
                    // could happen if user keeps menu open when we reply to terminate
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        NSLog("force terminating")
                        sender.terminate(nil)
                    }
                }
                Task { @MainActor in
                    // we don't care if this fails or succeeds, just give it best effort and terminate when done
                    NSLog("preparing to terminate")
                    await vmModel.tryStop()

                    NSLog("terminating from stop")
                    finishTerminate()
                }
                // also listen for stop events
                Task { @MainActor in
                    await vmModel.waitForStateEquals(.stopped)
                    NSLog("terminating from state")
                    finishTerminate()
                }

                return .terminateLater
            }
        } else {
            // we're not in menu bar mode, but menu bar is enabled.
            // enter menu bar mode by closing all windows, then cancel termination
            for window in NSApp.windows {
                if window.isUserFacing {
                    window.close()
                }
            }
            return .terminateCancel
        }
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
