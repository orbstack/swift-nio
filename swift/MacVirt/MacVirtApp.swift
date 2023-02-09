//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Sparkle

@main
struct MacVirtApp: App {
    @StateObject var model = VmViewModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    private let updaterController = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)

    var body: some Scene {
        WindowGroup {
            ContentView()
                    .environmentObject(model)
                    .frame(minWidth: 500, maxWidth: .infinity, minHeight: 300, maxHeight: .infinity)
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
        }.commands {
            CommandGroup(replacing: .newItem) {}
        }.handlesExternalEvents(matching: Set(arrayLiteral: "onboarding"))
        .windowStyle(.hiddenTitleBar)

        Settings {
            AppSettings(updaterController: updaterController)
                    .environmentObject(model)
        }
    }
}
