//
// Created by Danny Lin on 2/6/23.
//

import AppKit
import Defaults
import Foundation
import Sentry
import Sparkle
import SwiftUI
import UserNotifications

private let debugAlwaysCliBackground = false

class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var updaterController: SPUStandardUpdaterController?
    var actionTracker: ActionTracker!
    var windowTracker: WindowTracker!
    var vmModel: VmViewModel!

    private var menuBar: MenuBarController?

    func applicationWillFinishLaunching(_: Notification) {
        UNUserNotificationCenter.current().delegate = self

        // don't allow opening duplicate app instance - just activate old one
        // skip vmgr executables because there used to be a bug where it would open with same bundle id as GUI
        // need to keep the fix around for a long time due to update
        if !CommandLine.arguments.contains("--allow-duplicate"),
            let existingApp = NSRunningApplication.runningApplications(
                withBundleIdentifier: Bundle.main.bundleIdentifier!
            )
            .first(where: {
                $0 != NSRunningApplication.current
                    && $0.executableURL?.lastPathComponent != AppConfig.vmgrExeName
            })
        {
            print("App is already running")
            // activate first
            existingApp.activate(options: .activateIgnoringOtherApps)

            // send reopen event to open main window if necessary
            let targetDescriptor = NSAppleEventDescriptor(
                processIdentifier: existingApp.processIdentifier)
            AppleEvents.sendReopen(targetDescriptor: targetDescriptor)

            // NSApp.terminate doesn't work until applicationDidFinishLaunching,
            // but we want to avoid creating SwiftUI windows at all in order to avoid triggering .onAppear initLaunch
            exit(0)
        }

        // Current running instance does not reside in /Applications
        // Ask the user if they want to move it
        if shouldPromptToMoveApplication() {
            let alert = NSAlert()

            alert.messageText = "Move to Applications?"
            alert.informativeText = "I can move myself to the Applications folder if you'd like."

            alert.addButton(withTitle: "Move")
            alert.addButton(withTitle: "Don't Move")

            Defaults[.showMoveToApplications] = false  // no matter the choice, this'll be set to false. let's just do it here

            if alert.runModal() == .alertFirstButtonReturn {
                moveToApplicationsButtonClicked()
            }
        }

        vmModel.initLaunch()
    }

    func shouldPromptToMoveApplication() -> Bool {
        return !Bundle.main.bundlePath.hasPrefix("/Applications/")
            && Defaults[.showMoveToApplications]
    }

    @MainActor
    func moveToApplicationsButtonClicked() {  // Handles replacements, too
        Task {
            do {
                if vmModel.state == .running {
                    do {
                        try await vmModel.stop()
                    } catch {
                        #if DEBUG
                            throw StringError(
                                "Can't move app while background service is running (\(error))")
                        #else
                            throw StringError("Can't move app while background service is running")
                        #endif
                    }
                }

                let fm = FileManager.default
                let newAppURL = URL(fileURLWithPath: "/Applications/OrbStack.app")

                let needDestAuth = fm.fileExists(
                    atPath: newAppURL.path) /* if it already exists, then we'll just need permissions anyway (from testing.. :/) */
                let needAuth = needDestAuth || !fm.isWritableFile(atPath: "/Applications")

                if needAuth {
                    let deleteCommand = "rm -rf \(escapeShellArg(newAppURL.path))"
                    let copyCommand =
                        "cp -pR \(escapeShellArg(Bundle.main.bundlePath)) \(escapeShellArg(newAppURL.path))"

                    // Update LaunchServicesDB, touch can be used to update an app: https://github.com/sparkle-project/Sparkle/blob/3a9734538a38b35107962d6e8c5975cdabaeb56d/Sparkle/SUFileManager.m#L483
                    let touchCommand = "touch \(escapeShellArg(newAppURL.path))"
                    try runAsAdmin(
                        script: "\(deleteCommand) && \(copyCommand) && \(touchCommand)",
                        prompt: "OrbStack wants to move itself to the Applications folder")
                } else {
                    do {
                        try FileManager.default.moveItem(at: Bundle.main.bundleURL, to: newAppURL)
                    } catch {
                        try FileManager.default.copyItem(at: Bundle.main.bundleURL, to: newAppURL)
                    }

                    // update LaunchServicesDB (Sparkle does this, we're also using open+futimes to follow symlinks)
                    let fd = open(newAppURL.path, O_RDONLY | O_SYMLINK | O_CLOEXEC)
                    defer { close(fd) }

                    if fd != -1 {
                        futimes(fd, nil)
                    }
                }

                launchNewInstance(atURL: newAppURL)
            } catch {
                let alert = NSAlert()
                alert.messageText = "Failed to move OrbStack.app to /Applications"
                alert.informativeText = error.localizedDescription
                alert.addButton(withTitle: "OK")
                alert.window.isRestorable = false

                alert.runModal()
            }
        }
    }

    func launchNewInstance(atURL url: URL) {
        // to stop any wonky behaviour (because Bundle.main would point to the old location)
        // We re-launch the app from the /Applications path

        let conf = NSWorkspace.OpenConfiguration()
        conf.createsNewApplicationInstance = true
        conf.activates = true

        // OrbStack eliminates any duplicate instances, however, when re-launching the app,
        // we will have 2 instances just for a split second, before we terminate the old instance
        // so we pass this flag to keep the new instance happy
        conf.arguments = ["--allow-duplicate"]
        NSWorkspace.shared.openApplication(at: url, configuration: conf) { _, _ in
            exit(EXIT_SUCCESS)  // exit once the new app is launched
        }
    }

    @objc func noThanksButtonWasClicked(sender: NSButton) {
        sender.window?.close()
    }

    func applicationDidFinishLaunching(_: Notification) {
        if !AppConfig.debug {
            SentrySDK.start { options in
                options.dsn =
                    "https://c2ba2ba456ad14d70722cf2652bbca59@o120089.ingest.us.sentry.io/4504665519554560"
                options.tracesSampleRate = 0.0
                options.enableAppHangTracking = false
                options.appHangTimeoutInterval = 60  // 1 minute
            }
        }

        NSWindow.allowsAutomaticWindowTabbing = false

        // only show menu bar if started by CLI as background app, or as a login item
        // but if we haven't done onboarding, then do it now
        // can happen if users' first-run is via CLI
        let internalCliBackground = CommandLine.arguments.contains("--internal-cli-background")
        if Defaults[.onboardingCompleted]
            && (internalCliBackground || debugAlwaysCliBackground || launchedAsLoginItem())
        {
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

        // Menu bar status item
        menuBar = MenuBarController(
            updaterController: updaterController!,
            actionTracker: actionTracker, windowTracker: windowTracker,
            vmModel: vmModel)
        windowTracker.menuBar = menuBar

        // launch at login: close all windows. works with SMAppService
        // only if menu bar enabled. otherwise there will be no windows
        let launchEvent = NSAppleEventManager.shared().currentAppleEvent
        if launchEvent?.eventID == kAEOpenApplication
            && launchEvent?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue
                == keyAELaunchedAsLogInItem
            && Defaults[.globalShowMenubarExtra]
        {
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

        // in all other cases, we stop VM and then terminate
        // if users want to keep menu bar open, they should use Cmd-W or close window
        // Cmd-Q should quit the app
        // this fixes a lot of issues. for example, for logout/restart, macOS issues terminate,
        // and .terminateCancel causes immediate "OrbStack interrupted restart"

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
    }

    // on macOS 15.0+, SwiftUI reopen is broken unless app is compiled with Xcode 16+
    func applicationShouldHandleReopen(_: NSApplication, hasVisibleWindows: Bool) -> Bool {
        // normal behavior if hasVisibleWindows
        if hasVisibleWindows {
            return true
        }

        // if onboarding not completed, then open it
        if !Defaults[.onboardingCompleted] {
            OnboardingManager.maybeStartOnboarding()
            return false
        }

        // else open main (but use our openSubwindow logic to prevent dupes)
        NSWorkspace.openSubwindow(WindowID.main)
        return false
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        // WA: on macOS 13, app closes after main window is closed
        // that's consistent with docs because Window(main) is primary scene, but not consistent with macOS 14+ behavior where having additional scenes disables that behavior
        // https://developer.apple.com/documentation/swiftui/window#Use-a-window-as-the-main-scene
        false
    }

    func application(_: NSApplication, open urls: [URL]) {
        for url in urls {
            guard url.scheme == "orbstack" else {
                continue
            }

            // CLI, to trigger GUI to update
            // orbstack://update
            switch url.host {
            case WindowURL.update:
                updaterController?.updater.checkForUpdates()

            case WindowURL.completeAuth:
                if let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
                    let queryItems = components.queryItems,
                    let token = queryItems.first(where: { $0.name == "token" })?.value
                {
                    let state = vmModel.drmState
                    state.refreshToken = token
                    vmModel.drmState = state

                    // auth CLI will update token in vmgr
                }

            case WindowURL.settings:
                Self.showSettingsWindow()

            default:
                break
            }
        }
    }

    // called after new .windows is applied
    func applicationDidUpdate(_: Notification) {
        windowTracker.updateState()
    }

    // notification
    func userNotificationCenter(
        _: UNUserNotificationCenter, didReceive response: UNNotificationResponse
    ) async {
        if let url = (response.notification.request.content.userInfo["url"] as? String)?.toURL() {
            NSWorkspace.shared.open(url)
        }
    }

    static func showSettingsWindow() {
        // macOS 14 breaks the "showSettingsWindow" private API, so we have to do this...
        // simulate Cmd-, shortcut
        let fakeEvent = NSEvent.keyEvent(
            with: .keyDown,
            location: .zero,
            modifierFlags: [.command],
            timestamp: 0,
            windowNumber: 0,
            context: nil,
            characters: ",",
            charactersIgnoringModifiers: ",",
            isARepeat: false,
            keyCode: 0)!
        NSApp.mainMenu?.performKeyEquivalent(with: fakeEvent)
    }
}

// https://stackoverflow.com/a/74733681
private func launchedAsLoginItem() -> Bool {
    let event = NSAppleEventManager.shared().currentAppleEvent
    return event?.eventID == kAEOpenApplication
        && event?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue
            == keyAELaunchedAsLogInItem
}
