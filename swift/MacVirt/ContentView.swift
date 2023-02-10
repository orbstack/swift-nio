//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI

func bindOptionalBool<T>(_ binding: Binding<T?>) -> Binding<Bool> {
    Binding<Bool>(get: {
        binding.wrappedValue != nil
    }, set: {
        if !$0 {
            binding.wrappedValue = nil
        }
    })
}

struct ContentView: View {
    @Environment(\.controlActiveState) var controlActiveState
    @EnvironmentObject private var model: VmViewModel

    // SceneStorage inits too late
    @AppStorage("root.selectedTab") private var selection = "machines"
    @AppStorage("onboardingCompleted") private var onboardingCompleted = false
    @State private var startStopInProgress = false
    @StateObject private var windowHolder = WindowHolder()
    @State private var presentError = false

    var body: some View {
        NavigationView {
            let selBinding = Binding<String?>(get: {
                selection
            }, set: {
                if let sel = $0 {
                    selection = sel
                }
            })
            List(selection: selBinding) {
                NavigationLink(destination: DockerRootView()) {
                    Label("Docker", systemImage: "shippingbox")
                }.tag("docker")

                NavigationLink(destination: MachinesRootView()) {
                    Label("Machines", systemImage: "desktopcomputer")
                }.tag("machines")

                NavigationLink(destination: CommandsRootView()) {
                    Label("Commands", systemImage: "terminal")
                }.tag("cli")
            }
            .listStyle(.sidebar)
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                Button(action: toggleSidebar, label: {
                    Image(systemName: "sidebar.leading")
                })
                        .help("Toggle sidebar")
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    Task {
                        self.startStopInProgress = true
                        if model.state == .running {
                            await model.stop()
                        } else {
                            await model.start()
                        }
                        self.startStopInProgress = false
                    }
                }) {
                    Label(model.state == .running ? "Stop" : "Start", systemImage: "power")
                }
                        .disabled(startStopInProgress)
                        .help(model.state == .running ? "Stop everything" : "Start everything")
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    if #available(macOS 13, *) {
                        NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
                    } else {
                        NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
                    }
                }) {
                    Label("Settings", systemImage: "gearshape")
                }
                .help("Settings")
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .onAppear {
            if !onboardingCompleted {
                print("close w appear: \(windowHolder.window)")
                windowHolder.window?.close()
                NSWorkspace.shared.open(URL(string: "macvirt://onboarding")!)
            }
        }
        .onChange(of: windowHolder.window) {
            if !onboardingCompleted {
                print("close w change: \($0)")
                $0?.close()
            }
        }
        .task {
            await model.initLaunch()
        }
        .onChange(of: controlActiveState) { state in
            if state == .key {
                Task {
                    await model.tryRefreshList()
                }
            }
        }
        // error dialog
        .alert(isPresented: $presentError, error: model.error) { _ in
            Button("OK") {
                model.error = nil

                // quit if the error is fatal
                if model.state == .stopped {
                    NSApp.terminate(nil)
                }
            }
        } message: { error in
            if let msg = error.recoverySuggestion {
                Text(msg)
            }
        }
        .onReceive(model.$error, perform: {
            presentError = $0 != nil
        })
        .onChange(of: presentError) {
            if !$0 {
                model.error = nil
            }
        }
        .alert("Shell profile changed", isPresented: bindOptionalBool($model.presentProfileChanged)) {
        } message: {
            if let info = model.presentProfileChanged {
                Text("""
                    Your shell profile has been modified to add \(Constants.userAppName)’s command-line tools to your PATH.
                    To use them in existing shells, run the following command:

                    source \(info.profileRelPath)
                    """)
            }
        }
        .alert("Add tools to PATH", isPresented: bindOptionalBool($model.presentAddPaths)) {
        } message: {
            if let info = model.presentAddPaths {
                let list = info.paths.joined(separator: "\n")
                Text("""
                     To use \(Constants.userAppName)’s command-line tools, add the following directories to your PATH:

                     \(list)
                     """)
            }
        }
    }

    private func toggleSidebar() {
        NSApp.sendAction(#selector(NSSplitViewController.toggleSidebar(_:)), to: nil, from: nil)
    }
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
    }
}
