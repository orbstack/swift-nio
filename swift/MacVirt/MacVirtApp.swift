//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import Defaults
import Sparkle
import SwiftUI

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

    func feedURLString(for _: SPUUpdater) -> String? {
        // installID % 100
        let uuidBytes = readInstallID().uuid
        // take a big endian uint32 of the first 4 bytes
        let id4 =
            (UInt32(uuidBytes.0) << 24) | (UInt32(uuidBytes.1) << 16) | (UInt32(uuidBytes.2) << 8)
            | UInt32(uuidBytes.3)
        let bucket = id4 % 100

        #if arch(arm64)
            return "https://api-updates.orbstack.dev/arm64/appcast.xml?bucket=\(bucket)"
        #else
            return "https://api-updates.orbstack.dev/amd64/appcast.xml?bucket=\(bucket)"
        #endif
    }

    func allowedChannels(for _: SPUUpdater) -> Set<String> {
        Set(["stable", Defaults[.updatesOptinChannel]])
    }

    func updaterWillRelaunchApplication(_: SPUUpdater) {
        // bypass menu bar termination hook
        AppLifecycle.forceTerminate = true

        // run post-update script if needed to repair
        if let script = Bundle.main.path(forAuxiliaryExecutable: "hooks/_postupdate") {
            do {
                let task = try Process.run(
                    URL(fileURLWithPath: script), arguments: [Bundle.main.bundlePath])
                task.waitUntilExit()
            } catch {
                print("Failed to run post-update script: \(error)")
            }
        }
    }
}

enum AppLifecycle {
    static var forceTerminate = false
}

@main
struct MacVirtApp: App {
    @Environment(\.openWindow) private var openWindow

    // with StateObject, SwiftUI and AppDelegate get different instances
    // we need singleton so use ObservedObject
    @ObservedObject var vmModel = VmViewModel()
    @ObservedObject var actionTracker = ActionTracker()
    @ObservedObject var windowTracker = WindowTracker()

    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    @Default(.selectedTab) private var selectedTab

    private let delegate: UpdateDelegate
    private let updaterController: SPUStandardUpdaterController

    init() {
        delegate = UpdateDelegate()
        updaterController = SPUStandardUpdaterController(
            startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)
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

        Window("OrbStack", id: "main") {
            NewMainView()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
                .environmentObject(actionTracker)
                .frame(
                    minWidth: 550, maxWidth: .infinity, minHeight: 300,
                    maxHeight: .infinity
                )
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove default entry point from Window menu
        .commandsRemoved()
        .commands {
            Group {
                SidebarCommands()
                ToolbarCommands()
                TextEditingCommands()

                CommandGroup(after: .appInfo) {
                    CheckForUpdatesView(updater: updaterController.updater)
                }

                // Window() only shows up in the menu if there are multiple singleton windows
                CommandGroup(before: .singleWindowList) {
                    Button("OrbStack") {
                        openWindow(id: "main")
                    }
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

                    switch selectedTab {
                    case .dockerVolumes:
                        Divider()
                        Button("New Volume") {
                            vmModel.menuActionRouter.send(.newVolume)
                        }
                        .keyboardShortcut("n")

                        Button("Open Volumes") {
                            vmModel.menuActionRouter.send(.openVolumes)
                        }
                        .keyboardShortcut("o")
                    case .dockerImages:
                        Divider()
                        Button("Open Images") {
                            vmModel.menuActionRouter.send(.openImages)
                        }
                        .keyboardShortcut("o")
                    case .machines:
                        Divider()
                        Button("New Machine") {
                            vmModel.menuActionRouter.send(.newMachine)
                        }
                        .keyboardShortcut("n")
                    default:
                        EmptyView()
                    }
                }
            }

            // TODO: command to create container

            CommandMenu("Account") {
                Button("Sign In…") {
                    NSWorkspace.openSubwindow("authwindow")
                }
                .disabled(vmModel.drmState.isSignedIn)

                Button("Sign Out") {
                    Task { @MainActor in
                        await vmModel.trySignOut()
                    }
                }
                .disabled(!vmModel.drmState.isSignedIn)

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

            // keyboard tab nav for main window
            CommandMenu("Tab") {
                Group {
                    Button("Docker") {}
                        .disabled(true)

                    Button("Containers") {
                        selectedTab = .dockerContainers
                    }
                    .keyboardShortcut("1", modifiers: [.command])

                    Button("Volumes") {
                        selectedTab = .dockerVolumes
                    }
                    .keyboardShortcut("2", modifiers: [.command])

                    Button("Images") {
                        selectedTab = .dockerImages
                    }
                    .keyboardShortcut("3", modifiers: [.command])
                }

                Divider()

                Group {
                    Button("Kubernetes") {}
                        .disabled(true)

                    Button("Pods") {
                        selectedTab = .k8sPods
                    }
                    .keyboardShortcut("4", modifiers: [.command])

                    Button("Services") {
                        selectedTab = .k8sServices
                    }
                    .keyboardShortcut("5", modifiers: [.command])
                }

                Divider()

                Button("Linux") {}.disabled(true)

                Button("Machines") {
                    selectedTab = .machines
                }.keyboardShortcut("6", modifiers: [.command])

                Divider()

                Button("Help") {}.disabled(true)

                Button("Commands") {
                    selectedTab = .cli
                }.keyboardShortcut("7", modifiers: [.command])
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
        .defaultSize(width: 975, height: 650)

        Window("Setup", id: "onboarding") {
            OnboardingRootView()
                .environmentObject(vmModel)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
            // .frame(minWidth: 600, maxWidth: 600, minHeight: 400, maxHeight: 400)
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .handlesExternalEvents(matching: ["onboarding"])
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)

        WindowGroup(WindowTitles.containerLogsBase, id: "docker-container-logs") {
            DockerLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        // globally visible across all scenes!
        .commands {
            CommandGroup(after: .toolbar) {
                Defaults.Toggle("Word Wrap", key: .logsWordWrap)
            }
        }
        .handlesExternalEvents(matching: ["docker/container-logs/"])
        .defaultSize(width: 800, height: 600)
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup(WindowTitles.projectLogsBase, id: "docker-compose-logs") {
            DockerComposeLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["docker/project-logs/"])
        .defaultSize(width: 875, height: 625)  // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        WindowGroup(WindowTitles.podLogsBase, id: "k8s-pod-logs") {
            K8SPodLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["k8s/pod-logs/"])
        .defaultSize(width: 875, height: 625)  // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        Window("Migrate from Docker Desktop", id: "docker-migration") {
            DockerMigrationWindow()
                .environmentObject(vmModel)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["docker/migration"])
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)

        Group {
            Window("Diagnostic Report", id: "diagreport") {
                DiagReporterView(isBugReport: false)
                    .onAppear {
                        windowTracker.onWindowAppear()
                    }
            }
            // remove entry point from Window menu
            .commandsRemoved()
            .commands {
                CommandGroup(replacing: .newItem) {}
            }
            .handlesExternalEvents(matching: ["diagreport"])
            .windowStyle(.hiddenTitleBar)
            .windowResizability(.contentSize)

            Window("Report Bug", id: "bugreport") {
                DiagReporterView(isBugReport: true)
                    .onAppear {
                        windowTracker.onWindowAppear()
                    }
            }
            // remove entry point from Window menu
            .commandsRemoved()
            .commands {
                CommandGroup(replacing: .newItem) {}
            }
            .handlesExternalEvents(matching: ["bugreport"])
            .windowStyle(.hiddenTitleBar)
            .windowResizability(.contentSize)
        }

        Window("Sign In", id: "auth") {
            AuthView(sheetPresented: nil)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        // workaround for complete_auth matching this
        .handlesExternalEvents(matching: ["authwindow"])
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)

        Window("Send Feedback", id: "feedback") {
            FeedbackView()
                .onAppear {
                    windowTracker.onWindowAppear()
                }
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .handlesExternalEvents(matching: ["feedback"])
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)

        Settings {
            AppSettings(updaterController: updaterController)
                .environmentObject(vmModel)
                .onAppear {
                    windowTracker.onWindowAppear()
                }
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
