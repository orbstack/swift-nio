//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Sparkle
import Defaults

extension Scene {
    func windowResizabilityContentSize() -> some Scene {
        if #available(macOS 13, *) {
            return windowResizability(.contentSize)
        } else {
            return self
        }
    }

    func windowDefaultSize(width: CGFloat, height: CGFloat) -> some Scene {
        if #available(macOS 13, *) {
            return defaultSize(CGSize(width: width, height: height))
        } else {
            return self
        }
    }
}

extension View {
    func formStyleGrouped() -> some View {
        if #available(macOS 13, *) {
            return formStyle(.grouped)
        } else {
            return self
        }
    }
}

class UpdateDelegate: NSObject, SPUUpdaterDelegate {
    private func readInstallID() -> UUID {
        // match file like vmgr drm/device.go
        do {
            let oldID = try String(contentsOfFile: Files.installId)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            // try to parse it as UUID
            if let uuid = UUID(uuidString: oldID) {
                return uuid
            }
        } catch {
            // fallthrough
        }

        // write a new one
        let newID = UUID()
        do {
            try newID.uuidString
                .lowercased()
                .write(toFile: Files.installId, atomically: false, encoding: .utf8)
        } catch {
            NSLog("failed to write install ID: \(error)")
        }
        return newID
    }

    func feedURLString(for updater: SPUUpdater) -> String? {
        // installID % 100
        let uuidBytes = readInstallID().uuid
        // take a big endian uint32 of the first 4 bytes
        let id4 = (UInt32(uuidBytes.0) << 24) |
                (UInt32(uuidBytes.1) << 16) |
                (UInt32(uuidBytes.2) << 8) |
                UInt32(uuidBytes.3)
        let bucket = id4 % 100

        #if arch(arm64)
        return "https://api-updates.orbstack.dev/arm64/appcast.xml?bucket=\(bucket)"
        #else
        return "https://api-updates.orbstack.dev/amd64/appcast.xml?bucket=\(bucket)"
        #endif
    }

    func allowedChannels(for updater: SPUUpdater) -> Set<String> {
        Set(["stable", Defaults[.updatesOptinChannel]])
    }

    func updaterWillRelaunchApplication(_ updater: SPUUpdater) {
        // bypass menu bar termination hook
        AppLifecycle.forceTerminate = true

        // run post-update script if needed to repair
        if let script = Bundle.main.path(forAuxiliaryExecutable: "hooks/_postupdate") {
            do {
                let task = try Process.run(URL(fileURLWithPath: script), arguments: [Bundle.main.bundlePath])
                task.waitUntilExit()
            } catch {
                print("Failed to run post-update script: \(error)")
            }
        }
    }
}

struct AppLifecycle {
    static var forceTerminate = false
}

@main
struct MacVirtApp: App {
    // with StateObject, SwiftUI and AppDelegate get different instances
    // we need singleton so use ObservedObject
    @ObservedObject var vmModel = VmViewModel()
    @ObservedObject var actionTracker = ActionTracker()
    @ObservedObject var windowTracker = WindowTracker()

    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    private let delegate: UpdateDelegate
    private let updaterController: SPUStandardUpdaterController

    init() {
        delegate = UpdateDelegate()
        updaterController = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)
        // SUEnableSystemProfiling doesn't work?
        updaterController.updater.sendsSystemProfile = true

        appDelegate.updaterController = updaterController
        appDelegate.actionTracker = actionTracker
        appDelegate.windowTracker = windowTracker
        appDelegate.vmModel = vmModel

        for arg in CommandLine.arguments {
            if arg == "--check-updates" {
                updaterController.updater.checkForUpdates()
            }
        }
        
        // redirect logs
        #if !DEBUG
        freopen(Files.guiLog, "w+", stderr)
        #endif
    }

    var body: some Scene {
        /*
         * IMPORTANT:
         * ALL windows MUST report to WindowTracker in .onAppear!!!
         */

        WindowGroup {
            MainWindow()
            .environmentObject(vmModel)
            .environmentObject(windowTracker)
            .environmentObject(actionTracker)
            // workaround: default size uses min height on macOS 12, so this fixes default window size
            // on macOS 13+ we can set smaller min and use windowDefaultSize
            .frame(minWidth: 550, maxWidth: .infinity, minHeight: getMinHeight(), maxHeight: .infinity)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .commands {
            CommandGroup(replacing: .newItem) {}
            SidebarCommands()
            ToolbarCommands()
            TextEditingCommands()
            CommandGroup(after: .appInfo) {
                CheckForUpdatesView(updater: updaterController.updater)
            }
            CommandGroup(before: .systemServices) {
                Button("Invite a Friend") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev")!)
                }
                Button("Documentation") {
                    NSWorkspace.shared.open(URL(string: "https://docs.orbstack.dev")!)
                }
                Divider()
                Button("Report Bug") {
                    openBugReport()
                }
                Button("Request Feature") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                }
                Button("Send Feedback") {
                    openFeedbackWindow()
                }
                Divider()
            }
            CommandGroup(before: .importExport) {
                Button("Migrate Docker Data…") {
                    NSWorkspace.openSubwindow("docker/migration")
                }
            }
            //TODO command to create container

            CommandMenu("Account") {
                Button("Sign In…") {
                    NSWorkspace.openSubwindow("auth")
                }
                .disabled(vmModel.drmState.refreshToken != nil)

                Button("Sign Out") {
                    Task { @MainActor in
                        await vmModel.trySignOut()
                    }
                }
                .disabled(vmModel.drmState.refreshToken == nil)

                Divider()

                Button("Manage…") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
                }

                Button("Switch Organization…") {
                    NSWorkspace.openSubwindow("authwindow")
                }

                Divider()

                Button("Refresh") {
                    Task { @MainActor in
                        await vmModel.tryRefreshDrm()
                    }
                }
            }

            CommandGroup(after: .help) {
                Divider()

                Button("Website") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev")!)
                }
                Button("Documentation") {
                    NSWorkspace.shared.open(URL(string: "https://docs.orbstack.dev")!)
                }
                Button("Community") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/chat")!)
                }
                Button("Email") {
                    NSWorkspace.shared.open(URL(string: "mailto:support@orbstack.dev")!)
                }

                Divider()

                Group {
                    Button("Report Bug") {
                        openBugReport()
                    }
                    Button("Request Feature") {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                    }
                    Button("Send Feedback") {
                        openFeedbackWindow()
                    }
                }

                Divider()

                Button("Upload Diagnostics") {
                    openDiagReporter()
                }
            }
        }
        .handlesExternalEvents(matching: ["main", "docker/containers/", "docker/projects/"])
        .windowDefaultSize(width: 725, height: 600)

        WindowGroup("Setup", id: "onboarding") {
            OnboardingRootView()
            .environmentObject(vmModel)
            .onAppear {
                windowTracker.onWindowAppear()
            }
            //.frame(minWidth: 600, maxWidth: 600, minHeight: 400, maxHeight: 400)
        }
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .handlesExternalEvents(matching: ["onboarding"])
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        WindowGroup(WindowTitles.containerLogsBase, id: "docker-container-logs") {
            DockerLogsWindow()
            .environmentObject(vmModel)
            .environmentObject(windowTracker)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/container-logs/"])
        .windowDefaultSize(width: 800, height: 600)
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup(WindowTitles.projectLogsBase, id: "docker-compose-logs") {
            DockerComposeLogsWindow()
            .environmentObject(vmModel)
            .environmentObject(windowTracker)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/project-logs/"])
        .windowDefaultSize(width: 875, height: 625) // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup(WindowTitles.podLogsBase, id: "k8s-pod-logs") {
            K8SPodLogsWindow()
            .environmentObject(vmModel)
            .environmentObject(windowTracker)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["k8s/pod-logs/"])
        .windowDefaultSize(width: 875, height: 625) // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup("Migrate from Docker Desktop", id: "docker-migration") {
            DockerMigrationWindow()
            .environmentObject(vmModel)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/migration"])
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        Group {
            WindowGroup("Diagnostic Report", id: "diagreport") {
                DiagReporterView(isBugReport: false)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
            }
            .commands {
                CommandGroup(replacing: .newItem) {
                }
            }
            .handlesExternalEvents(matching: ["diagreport"])
            .windowStyle(.hiddenTitleBar)
            .windowResizabilityContentSize()

            WindowGroup("Report Bug", id: "bugreport") {
                DiagReporterView(isBugReport: true)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
            }
            .commands {
                CommandGroup(replacing: .newItem) {
                }
            }
            .handlesExternalEvents(matching: ["bugreport"])
            .windowStyle(.hiddenTitleBar)
            .windowResizabilityContentSize()
        }

        WindowGroup("Sign In", id: "auth") {
            AuthView(sheetPresented: nil)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        // workaround for complete_auth matching this
        .handlesExternalEvents(matching: ["authwindow"])
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        WindowGroup("Send Feedback", id: "feedback") {
            FeedbackView()
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .handlesExternalEvents(matching: ["feedback"])
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        Settings {
            AppSettings(updaterController: updaterController)
            .environmentObject(vmModel)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
    }

    private func getMinHeight() -> CGFloat {
        if #available(macOS 13, *) {
            return 300
        } else {
            return 500
        }
    }
}

func getConfigDir() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return home + "/.orbstack"
}

func openDiagReporter() {
    NSWorkspace.openSubwindow("diagreport")
}

func openBugReport() {
    NSWorkspace.openSubwindow("bugreport")
}

func openFeedbackWindow() {
    NSWorkspace.openSubwindow("feedback")
}
