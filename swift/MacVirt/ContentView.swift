//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import UserNotifications

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
    @AppStorage("root.selectedTab") private var selection = "docker"
    @AppStorage("onboardingCompleted") private var onboardingCompleted = false
    @State private var startStopInProgress = false
    @StateObject private var windowHolder = WindowHolder()
    @State private var presentError = false
    @State private var pendingClose = false

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

                NavigationLink(destination: FilesRootView()) {
                    Label("Files", systemImage: "folder")
                }.tag("files")
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
            
            ToolbarItem(placement: .automatic) {
                if model.state == .running && selection == "machines" {
                    Button(action: {
                        model.presentCreate = true
                    }) {
                        Label("New Machine", systemImage: "plus")
                    }.popover(isPresented: $model.presentCreate) {
                        CreateContainerView(isPresented: $model.presentCreate, creatingCount: $model.creatingCount)
                    }
                    .help("New machine")
                }
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .onAppear {
            if !onboardingCompleted {
                pendingClose = true
                NSWorkspace.shared.open(URL(string: "macvirt://onboarding")!)
            }
        }
        .task {
            let center = UNUserNotificationCenter.current()
            center.requestAuthorization(options: [.alert, .sound, .badge]) { granted, error in
                print("notification permission granted: \(granted)")
            }

            await model.initLaunch()
        }
        .onChange(of: controlActiveState) { state in
            if state == .key {
                Task {
                    print("try refresh: root view - key")
                    await model.tryRefreshList()
                }
            }
        }
        // error dialog
        .alert(isPresented: $presentError, error: model.error) { _ in
            Button("OK") {
                model.dismissError()

                // quit if the error is fatal
                if model.state == .stopped && !model.reachedRunning {
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
                model.dismissError()
            }
        }
        .alert("Shell profile changed", isPresented: bindOptionalBool($model.presentProfileChanged)) {
        } message: {
            if let info = model.presentProfileChanged {
                Text("""
                    \(Constants.userAppName)’s command-line tools have been added to your PATH.
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
