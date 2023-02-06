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
        }.commands {
            CommandGroup(replacing: .newItem) {}
        }

        Settings {
            AppSettings()
        }
    }
}
