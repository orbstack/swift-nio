//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 11/19/24.
//

import Defaults
import Sparkle
import SwiftUI

private typealias SingletonWindow = Window

struct MacVirtApp14: App {
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
        // check OS version before any async code can run
        // unfortunately, this is a bit too early, causing the "Update" button to be blue (AppColor not loaded from asset catalog yet)
        // but applicationWillFinishLaunching is too late and shows unrendered state-restored SwiftUI windows in the background
        VersionGate.maybeShowMacOS15BetaAlert()

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
        // adds "About" command to menu
        SingletonWindow("OrbStack", id: "main") {
            NewMainView()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
                .environmentObject(actionTracker)
                .frame(
                    minWidth: 550, maxWidth: .infinity, minHeight: 300,
                    maxHeight: .infinity
                )
        }
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
                        openWindow.call(id: "main")
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
                        openWindow.call(id: WindowID.bugReport)
                    }
                    Button("Request Feature") {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                    }
                    Button("Send Feedback") {
                        openWindow.call(id: WindowID.feedback)
                    }
                    Divider()
                }

                CommandGroup(before: .importExport) {
                    Button("Migrate Docker Containers…") {
                        openWindow.call(id: WindowID.migrateDocker)
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

                        Button("Import Volume") {
                            vmModel.menuActionRouter.send(.importVolume)
                        }
                        .keyboardShortcut("i")
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
                        Button("Import Machine") {
                            vmModel.menuActionRouter.send(.importMachine)
                        }
                        .keyboardShortcut("i")
                    default:
                        EmptyView()
                    }
                }
            }

            // TODO: command to create container

            CommandMenu("Account") {
                Button("Sign In…") {
                    openWindow.call(id: WindowID.signIn)
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
                    openWindow.call(id: WindowID.signIn)
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
                        openWindow.call(id: WindowID.bugReport)
                    }
                    Button("Request Feature") {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                    }
                    Button("Send Feedback") {
                        openWindow.call(id: WindowID.feedback)
                    }
                }

                Divider()

                Button("Upload Diagnostics") {
                    openWindow.call(id: WindowID.diagReport)
                }
            }
        }
        .handlesExternalEvents(matching: ["main", "docker/containers/", "docker/projects/"])
        .defaultSize(width: 975, height: 650)

        SingletonWindow("Setup", id: WindowID.onboarding) {
            OnboardingRootView()
                .environmentObject(vmModel)
            // .frame(minWidth: 600, maxWidth: 600, minHeight: 400, maxHeight: 400)
        }
        // opened by AppDelegate, which doesn't have OpenWindowAction
        .handlesExternalEvents(matching: [WindowID.onboarding])
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)

        WindowGroup(WindowTitles.containerLogsBase, id: "docker-container-logs") {
            DockerLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
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
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["k8s/pod-logs/"])
        .defaultSize(width: 875, height: 625)  // extra side for sidebar
        .windowToolbarStyle(.unifiedCompact)

        SingletonWindow("Migrate from Docker Desktop", id: WindowID.migrateDocker) {
            DockerMigrationWindow()
                .environmentObject(vmModel)
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        // WA: on unrecognized URL (e.g. orbstack://update), SwiftUI opens the first window with no .handlesExternalEvents
        // we handle some URLs in the AppKit delegate, so to prevent that behavior, register an empty set of URL matches on every window
        .handlesExternalEvents(matching: [])

        Group {
            SingletonWindow("Diagnostic Report", id: WindowID.diagReport) {
                DiagReporterView(isBugReport: false)
            }
            // remove entry point from Window menu
            .commandsRemoved()
            .commands {
                CommandGroup(replacing: .newItem) {}
            }
            .windowStyle(.hiddenTitleBar)
            .windowResizability(.contentSize)
            .handlesExternalEvents(matching: [])

            SingletonWindow("Report Bug", id: WindowID.bugReport) {
                DiagReporterView(isBugReport: true)
            }
            // remove entry point from Window menu
            .commandsRemoved()
            .commands {
                CommandGroup(replacing: .newItem) {}
            }
            .windowStyle(.hiddenTitleBar)
            .windowResizability(.contentSize)
            .handlesExternalEvents(matching: [])

            SingletonWindow("Reset Data", id: WindowID.resetData) {
                ResetDataView()
                    .environmentObject(vmModel)
            }
            // remove entry point from Window menu
            .commandsRemoved()
            .commands {
                CommandGroup(replacing: .newItem) {}
            }
            .windowStyle(.hiddenTitleBar)
            .windowResizability(.contentSize)
            .handlesExternalEvents(matching: [])
        }

        SingletonWindow("Sign In", id: WindowID.signIn) {
            AuthView(sheetPresented: nil)
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        .handlesExternalEvents(matching: [])

        SingletonWindow("Send Feedback", id: WindowID.feedback) {
            FeedbackView()
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .commands {
            CommandGroup(replacing: .newItem) {}
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        .handlesExternalEvents(matching: [])

        Settings {
            AppSettings(updaterController: updaterController)
                .environmentObject(vmModel)
        }
        .windowToolbarStyle(.unified)
        .windowStyle(.titleBar)
    }
}
