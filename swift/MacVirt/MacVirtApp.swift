//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Connect

@main
struct MacVirtApp: App {
    @StateObject var model = VmViewModel()

    init() {
        model.earlyInit()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                    .environmentObject(model)
                    .frame(minWidth: 500, maxWidth: .infinity, minHeight: 300, maxHeight: .infinity)
        }.commands {
            CommandGroup(replacing: .newItem) {}
            SidebarCommands()
            //TODO command to create container
        }

        WindowGroup("Setup") {
            OnboardingRootView()
                    .environmentObject(model)
                    .frame(minWidth: 500, maxWidth: .infinity, minHeight: 300, maxHeight: .infinity)
        }.commands {
            CommandGroup(replacing: .newItem) {}
        }.handlesExternalEvents(matching: Set(arrayLiteral: "onboarding"))

        Settings {
            AppSettings()
        }
    }
}
