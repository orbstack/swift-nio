//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Sparkle

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
    func feedURLString(for updater: SPUUpdater) -> String? {
        #if arch(arm64)
        "https://api-updates.orbstack.dev/arm64/appcast.xml"
        #else
        "https://api-updates.orbstack.dev/amd64/appcast.xml"
        #endif
    }

    func allowedChannels(for updater: SPUUpdater) -> Set<String> {
        Set(["beta"])
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
    @ObservedObject var model = VmViewModel()
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
        appDelegate.vmModel = model

        for arg in CommandLine.arguments {
            if arg == "--check-updates" {
                updaterController.updater.checkForUpdates()
            }
        }
    }

    var body: some Scene {
        /*
         * IMPORTANT:
         * ALL windows MUST report to WindowTracker in .onAppear!!!
         */

        WindowGroup {
            ContentView()
            .environmentObject(model)
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
                    NSWorkspace.shared.open(URL(string: "orbstack://docker/migration")!)
                }
            }
            //TODO command to create container

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

                Button("Collect Diagnostics") {
                    openDiagReporter()
                }
            }
        }
        .handlesExternalEvents(matching: ["main", "docker/containers/", "docker/projects/"])
        .windowDefaultSize(width: 725, height: 600)

        WindowGroup("Setup", id: "onboarding") {
            OnboardingRootView()
            .environmentObject(model)
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
            .environmentObject(model)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/container-logs/"])
        .windowDefaultSize(width: 800, height: 600)
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup(WindowTitles.projectLogsBase, id: "docker-compose-logs") {
            DockerComposeLogsWindow()
            .environmentObject(model)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/project-logs/"])
        .windowDefaultSize(width: 875, height: 625) // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup("Migrate from Docker Desktop", id: "docker-migration") {
            DockerMigrationWindow()
            .environmentObject(model)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .handlesExternalEvents(matching: ["docker/migration"])
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        WindowGroup("Diagnostic Report", id: "diagreport") {
            DiagReporterView(isBugReport: false)
            .onAppear {
                windowTracker.onWindowAppear()
            }
        }
        .commands {
            CommandGroup(replacing: .newItem) {}
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
            CommandGroup(replacing: .newItem) {}
        }
        .handlesExternalEvents(matching: ["bugreport"])
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
            .environmentObject(model)
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
    NSWorkspace.shared.open(URL(string: "orbstack://diagreport")!)
}

func openBugReport() {
    NSWorkspace.shared.open(URL(string: "orbstack://bugreport")!)
}

func openFeedbackWindow() {
    NSWorkspace.shared.open(URL(string: "orbstack://feedback")!)
}
