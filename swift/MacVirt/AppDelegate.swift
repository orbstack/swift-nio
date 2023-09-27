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
        // skip vmgr executables because there used to be a bug where it would open with same bundle id as GUI
        // need to keep the fix around for a long time due to update
        if let existingApp = NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier!)
                .first(where: { $0 != NSRunningApplication.current && $0.executableURL?.lastPathComponent != AppConfig.vmgrExeName }) {
            print("App is already running")
            // activate first
            existingApp.activate(options: .activateIgnoringOtherApps)

            // send reopen event to open main window if necessary
            let targetDescriptor = NSAppleEventDescriptor(processIdentifier: existingApp.processIdentifier)
            AppleEvents.sendReopen(targetDescriptor: targetDescriptor)

            // NSApp.terminate doesn't work until applicationDidFinishLaunching,
            // but we want to avoid creating SwiftUI windows at all in order to avoid triggering .onAppear initLaunch
            exit(0)
        }
    }

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        if !AppConfig.debug {
            SentrySDK.start { options in
                options.dsn = "https://fc975a3abcaa9803fc2405d8b4bb3b62@o120089.ingest.sentry.io/4504665519554560"
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

        // launch at login: close all windows. works with SMAppService
        // only if menu bar enabled. otherwise there will be no windows
        let launchEvent = NSAppleEventManager.shared().currentAppleEvent
        if launchEvent?.eventID == kAEOpenApplication &&
                   launchEvent?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem &&
                   Defaults[.globalShowMenubarExtra] {
            for window in NSApp.windows {
                if window.isUserFacing {
                    window.close()
                }
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

                // and finally, a timeout
                DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
                    NSLog("timeout terminating")
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
            guard url.scheme == "orbstack" else {
                continue
            }

            // CLI, to trigger GUI to update
            // orbstack://update
            switch url.host {
            case "update":
                updaterController?.updater.checkForUpdates()

            case "complete_auth":
                NSApp.activate(ignoringOtherApps: true)

                if let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
                   let queryItems = components.queryItems,
                   let token = queryItems.first(where: { $0.name == "token" })?.value {
                    var state = vmModel.drmState
                    state.refreshToken = token
                    vmModel.drmState = state

                    // auth CLI will update token in vmgr
                }

            case "settings":
                if #available(macOS 13, *) {
                    NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
                } else {
                    NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
                }

            default:
                break
            }
        }
    }

    // notification
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        if let url = (response.notification.request.content.userInfo["url"] as? String)?.toURL() {
            NSWorkspace.shared.open(url)
        }
    }
}
