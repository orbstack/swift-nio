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

                    Divider()

                    Button {
                        appDelegate.showSettingsWindow()
                    } label: {
                        Label("Settings…", systemImage: "gear")
                    }.keyboardShortcut(",")
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
                    Divider()
                }

                CommandGroup(before: .importExport) {
                    Button("Migrate from Docker Desktop…") {
                        openWindow.call(id: WindowID.migrateDocker)
                    }

                    switch selectedTab {
                    case .dockerVolumes:
                        Divider()
                        Button {
                            vmModel.menuActionRouter.send(.newVolume)
                        } label: {
                            Label("New Volume", systemImage: "plus")
                        }
                        .keyboardShortcut("n")

                        Button {
                            vmModel.menuActionRouter.send(.openVolumes)
                        } label: {
                            Label("Open Volumes", systemImage: "folder")
                        }
                        .keyboardShortcut("o")

                        Button {
                            vmModel.menuActionRouter.send(.importVolume)
                        } label: {
                            Label("Import Volume…", systemImage: "square.and.arrow.down")
                        }
                        .keyboardShortcut("i")
                    case .dockerImages:
                        Divider()
                        Button {
                            vmModel.menuActionRouter.send(.openImages)
                        } label: {
                            Label("Open Images", systemImage: "folder")
                        }
                        .keyboardShortcut("o")
                        Button {
                            vmModel.menuActionRouter.send(.importImage)
                        } label: {
                            Label("Import Image…", systemImage: "square.and.arrow.down")
                        }
                        .keyboardShortcut("i")
                    case .dockerNetworks:
                        Divider()
                        Button {
                            vmModel.menuActionRouter.send(.newNetwork)
                        } label: {
                            Label("New Network", systemImage: "plus")
                        }
                    case .machines:
                        Divider()
                        Button {
                            vmModel.menuActionRouter.send(.newMachine)
                        } label: {
                            Label("New Machine", systemImage: "plus")
                        }
                        .keyboardShortcut("n")
                        Button {
                            vmModel.menuActionRouter.send(.importMachine)
                        } label: {
                            Label("Import Machine…", systemImage: "square.and.arrow.down")
                        }
                        .keyboardShortcut("i")
                    default:
                        EmptyView()
                    }
                }
            }

            // TODO: command to create container

            CommandMenu("Account") {
                if vmModel.drmState.isSignedIn {
                    Button {
                        Task { @MainActor in
                            await vmModel.trySignOut()
                        }
                    } label: {
                        Label("Sign Out", systemImage: "person.crop.circle")
                    }
                } else {
                    Button {
                        openWindow.call(id: WindowID.signIn)
                    } label: {
                        Label("Sign In…", systemImage: "person.crop.circle")
                    }
                }

                Divider()

                Button {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
                } label: {
                    Label("Manage…", systemImage: "pencil")
                }

                Button {
                    openWindow.call(id: WindowID.signIn)
                } label: {
                    Label("Switch Organization…", systemImage: "arrow.left.arrow.right")
                }

                Divider()

                Button {
                    Task { @MainActor in
                        await vmModel.tryRefreshDrm()
                    }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }

            // keyboard tab nav for main window
            CommandMenu("Tab") {
                Section("Docker") {
                    Button {
                        selectedTab = .dockerContainers
                    } label: {
                        Label("Containers", systemImage: "shippingbox")
                    }
                    .keyboardShortcut("1", modifiers: [.command])

                    Button {
                        selectedTab = .dockerVolumes
                    } label: {
                        Label("Volumes", systemImage: "externaldrive")
                    }
                    .keyboardShortcut("2", modifiers: [.command])

                    Button {
                        selectedTab = .dockerImages
                    } label: {
                        Label("Images", systemImage: "zipper.page")
                    }
                    .keyboardShortcut("3", modifiers: [.command])

                    Button {
                        selectedTab = .dockerNetworks
                    } label: {
                        Label(
                            "Networks", systemImage: "point.3.filled.connected.trianglepath.dotted")
                    }
                    .keyboardShortcut("4", modifiers: [.command])
                }

                Divider()

                Section("Kubernetes") {
                    Button {
                        selectedTab = .k8sPods
                    } label: {
                        Label("Pods", systemImage: "helm")
                    }
                    .keyboardShortcut("5", modifiers: [.command])

                    Button {
                        selectedTab = .k8sServices
                    } label: {
                        Label("Services", systemImage: "network")
                    }
                    .keyboardShortcut("6", modifiers: [.command])
                }

                Divider()

                Section("Linux") {
                    Button {
                        selectedTab = .machines
                    } label: {
                        Label("Machines", systemImage: "desktopcomputer")
                    }.keyboardShortcut("7", modifiers: [.command])
                }

                Divider()

                Section("General") {
                    Button {
                        selectedTab = .activityMonitor
                    } label: {
                        Label("Activity Monitor", systemImage: "chart.xyaxis.line")
                    }.keyboardShortcut("8", modifiers: [.command])

                    Button {
                        selectedTab = .cli
                    } label: {
                        Label("Commands", systemImage: "terminal")
                    }.keyboardShortcut("9", modifiers: [.command])
                }
            }

            CommandGroup(after: .help) {
                Divider()

                Button {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev")!)
                } label: {
                    Label("Website", systemImage: "network")
                }
                Button {
                    NSWorkspace.shared.open(URL(string: "https://docs.orbstack.dev")!)
                } label: {
                    Label("Documentation", systemImage: "book.closed")
                }
                Button {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/chat")!)
                } label: {
                    Label("Community", systemImage: "message")
                }
                Button {
                    NSWorkspace.shared.open(URL(string: "mailto:support@orbstack.dev")!)
                } label: {
                    Label("Email", systemImage: "envelope")
                }

                Divider()

                Group {
                    Button {
                        openWindow.call(id: WindowID.bugReport)
                    } label: {
                        Label("Report Bug", systemImage: "exclamationmark.triangle")
                    }
                    Button {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                    } label: {
                        Label("Request Feature", systemImage: "lightbulb")
                    }
                    Button {
                        openWindow.call(id: WindowID.feedback)
                    } label: {
                        Label("Send Feedback", systemImage: "paperplane")
                    }
                }

                Divider()

                Button {
                    openWindow.call(id: WindowID.diagReport)
                } label: {
                    Label("Upload Diagnostics", systemImage: "ladybug")
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

        WindowGroup(WindowTitles.projectLogsBase, id: "docker-compose-logs") {
            DockerComposeLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["docker/project-logs/"])
        .defaultSize(width: 875, height: 625)  // extra side for sidebar

        WindowGroup(WindowTitles.podLogsBase, id: "k8s-pod-logs") {
            K8SPodLogsWindow()
                .environmentObject(vmModel)
                .environmentObject(windowTracker)
        }
        // remove entry point from Window menu
        .commandsRemoved()
        .handlesExternalEvents(matching: ["k8s/pod-logs/"])
        .defaultSize(width: 875, height: 625)  // extra side for sidebar

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
    }
}
