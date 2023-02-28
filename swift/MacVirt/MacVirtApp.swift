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
    @StateObject var model = VmViewModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    private let delegate: UpdateDelegate
    private let updaterController: SPUStandardUpdaterController

    init() {
        delegate = UpdateDelegate()
        updaterController = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)

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
                    // workaround: default size uses min height, so this fixes default window size
                    .frame(minWidth: 400, maxWidth: .infinity, minHeight: 500, maxHeight: .infinity)
        }.commands {
            CommandGroup(replacing: .newItem) {}
            SidebarCommands()
            CommandGroup(after: .appInfo) {
                CheckForUpdatesView(updater: updaterController.updater)
            }
            CommandGroup(after: .appSettings) {
                Button("Show Logs") {
                    // get home folder
                    let home = FileManager.default.homeDirectoryForCurrentUser.path
                    NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: home + "/.orbstack/log")
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
                Button("Report a Bug") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues")!)
                }
                Button("Request a Feature") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues")!)
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
                Button("Report a Bug") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues")!)
                }
                Button("Request a Feature") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues")!)
                }
            }
        }.handlesExternalEvents(matching: Set(arrayLiteral: "main"))

        WindowGroup("Setup", id: "onboarding") {
            OnboardingRootView()
                    .environmentObject(model)
            //.frame(minWidth: 600, maxWidth: 600, minHeight: 400, maxHeight: 400)
        }.commands {
                    CommandGroup(replacing: .newItem) {}
                }.handlesExternalEvents(matching: Set(arrayLiteral: "onboarding"))
        .windowStyle(.hiddenTitleBar)
        .windowResizabilityContentSize()

        Settings {
            AppSettings(updaterController: updaterController)
                    .environmentObject(model)
        }
    }
}
