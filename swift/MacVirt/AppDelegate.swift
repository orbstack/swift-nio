//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import AppKit
import Sentry
import Sparkle
import UserNotifications
import Defaults

private let debugAlwaysCliBackground = false

class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var updaterController: SPUStandardUpdaterController?
    var actionTracker: ActionTracker!
    var windowTracker: WindowTracker!
    var vmModel: VmViewModel!

    private var menuBar: MenuBarController?

    func applicationWillFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self

        // don't allow opening duplicate app instance - just activate old one
        if let existingApp = NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier!)
                .first(where: { $0 != NSRunningApplication.current }) {
            print("App is already running")
            existingApp.activate(options: .activateIgnoringOtherApps)
            // NSApp.terminate doesn't work until applicationDidFinishLaunching,
            // but we want to avoid creating SwiftUI windows at all in order to avoid triggering .onAppear initLaunch
            exit(0)
        }
    }

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        if !AppConfig.debug {
            SentrySDK.start { options in
                options.dsn = "https://dd291d184e7941fbb20071f90b09792b@o120089.ingest.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
                options.appHangTimeoutInterval = 60 // 1 minute
            }
        }

        NSWindow.allowsAutomaticWindowTabbing = false

        for arg in CommandLine.arguments {
            // only show menu bar if started by CLI as background app
            // but if we haven't done onboarding, then do it now
            // can happen if users' first-run is via CLI
            if (arg == "--internal-cli-background" || debugAlwaysCliBackground) && Defaults[.onboardingCompleted] {
                // don't steal focus
                NSApp.setActivationPolicy(.accessory)

                // close all user-facing windows, regardless of menu bar
                // this means that w/o menubar, we'll get an empty app in the Dock
                for window in NSApp.windows {
                    if window.isUserFacing {
                        window.close()
                    }
                }

                NSApp.hide(nil)
                NSApp.deactivate()
            }
        }

        // Menu bar status item
        menuBar = MenuBarController(updaterController: updaterController!,
                actionTracker: actionTracker, windowTracker: windowTracker,
                vmModel: vmModel)
        windowTracker.menuBar = menuBar

        // close any leftover log windows.
        // TODO fix isRestorable WindowHolder flag
        for window in NSApp.windows {
            // match name to catch empty (no selection) windows only
            if window.title == WindowTitles.logs {
                window.close()
            }
        }

        // open onboarding and close other windows (e.g. main) if needed
        // vmgr will still start - onAppear already fired for main
        OnboardingManager.maybeStartOnboarding()
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if AppLifecycle.forceTerminate {
            // user explicitly force quit, so just terminate
            return .terminateNow
        }

        if sender.activationPolicy() == .accessory || !Defaults[.globalShowMenubarExtra] || menuBar?.quitInitiated == true {
            // we're already in menu bar mode, or menu bar is disabled, or user initiated quit from menu bar.
            // in all cases, we stop VM and then terminate

            // exception: if user enabled "stay in background" then don't stop VM
            if Defaults[.globalStayInBackground] {
                return .terminateNow
            }

            // also exception if option key pressed
            if CGKeyCode.optionKeyPressed {
                return .terminateNow
            }

            // is VM running?
            if vmModel.state == .stopped {
                // already fully stopped, so safe to terminate now
                return .terminateNow
            } else {
                // VM is running, so do a graceful shutdown and terminate later
                func finishTerminate() {
                    menuBar?.hide()
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

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        // normal behavior if hasVisibleWindows
        if hasVisibleWindows {
            return true
        }

        // enter regular mode if we're exiting menu bar mode
        windowTracker.setPolicy(.regular)

        // if onboarding not completed, then open it
        if !Defaults[.onboardingCompleted] {
            OnboardingManager.maybeStartOnboarding()
            return false
        }

        // else let SwiftUI open main
        return true
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
