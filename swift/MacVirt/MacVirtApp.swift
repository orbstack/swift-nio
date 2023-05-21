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
}

@main
struct MacVirtApp: App {
    // with StateObject, SwiftUI and AppDelegate get different instances
    // we need singleton so use ObservedObject
    @ObservedObject var model = VmViewModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    private let delegate: UpdateDelegate
    private let updaterController: SPUStandardUpdaterController

    init() {
        delegate = UpdateDelegate()
        updaterController = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)
        appDelegate.updaterController = updaterController
        appDelegate.vmModel = model

        for arg in CommandLine.arguments {
            if arg == "--check-updates" {
                updaterController.updater.checkForUpdates()
            }
        }
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                    .environmentObject(model)
                    // workaround: default size uses min height on macOS 12, so this fixes default window size
                    // on macOS 13+ we can set smaller min and use windowDefaultSize
                    .frame(minWidth: 550, maxWidth: .infinity, minHeight: getMinHeight(), maxHeight: .infinity)
        }.commands {
            CommandGroup(replacing: .newItem) {}
            SidebarCommands()
            ToolbarCommands()
            CommandGroup(after: .appInfo) {
                CheckForUpdatesView(updater: updaterController.updater)
            }
            CommandGroup(after: .appSettings) {
                Button("Show Logs") {
                    openLogsFolder()
                }
                Button("Invite a Friend") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/waitlist")!)
                }
            }
            CommandGroup(before: .systemServices) {
                Button("Website") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev")!)
                }
                Button("Documentation") {
                    NSWorkspace.shared.open(URL(string: "https://docs.orbstack.dev")!)
                }
                Divider()
                Button("Report Bug") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/bug")!)
                }
                Button("Request Feature") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                }
                Divider()
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
                Divider()
                Button("Report Bug") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/bug")!)
                }
                Button("Request Feature") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
                }
            }
        }.handlesExternalEvents(matching: Set(arrayLiteral: "main"))
        .windowDefaultSize(width: 750, height: 600)

        WindowGroup("Setup", id: "onboarding") {
            OnboardingRootView()
                    .environmentObject(model)
            //.frame(minWidth: 600, maxWidth: 600, minHeight: 400, maxHeight: 400)
        }.commands {
                    CommandGroup(replacing: .newItem) {}
                }.handlesExternalEvents(matching: Set(arrayLiteral: "onboarding"))
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        WindowGroup("Logs", id: "docker-container-logs") {
            DockerLogsWindow()
                    .environmentObject(model)
        }.handlesExternalEvents(matching: Set(arrayLiteral: "docker/containers/logs/", "docker/projects/logs/"))
        .windowDefaultSize(width: 750, height: 500)

        Settings {
            AppSettings(updaterController: updaterController)
                    .environmentObject(model)
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

func openLogsFolder() {
    NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: getConfigDir() + "/log")
}

func openReportWindows() {
    openLogsFolder()
    // open github
    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/bug")!)
}