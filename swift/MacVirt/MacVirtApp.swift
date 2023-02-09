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
        if #available(macOS 13.0, *) {
            return windowResizability(.contentSize)
        } else {
            return self
        }
    }

    func windowDefaultSize(width: CGFloat, height: CGFloat) -> some Scene {
        if #available(macOS 13.0, *) {
            return defaultSize(CGSize(width: width, height: height))
        } else {
            return self
        }
    }
}

@main
struct MacVirtApp: App {
    @StateObject var model = VmViewModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    private let updaterController = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)

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
            //TODO command to create container
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
