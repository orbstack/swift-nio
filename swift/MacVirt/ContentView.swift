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
    @State private var collapsed = false

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
                Section(header: Text("Docker")) {
                    NavigationLink(destination: DockerContainersRootView()) {
                        Label("Containers", systemImage: "shippingbox")
                    }.tag("docker")

                    NavigationLink(destination: DockerVolumesRootView()) {
                        Label("Volumes", systemImage: "externaldrive")
                    }.tag("docker-volumes")
                }

                Section(header: Text("Linux")) {
                    NavigationLink(destination: MachinesRootView()) {
                        Label("Machines", systemImage: "desktopcomputer")
                    }.tag("machines")

                    NavigationLink(destination: FilesRootView()) {
                        Label("Files", systemImage: "folder")
                    }.tag("files")
                }

                Section(header: Text("Info")) {
                    NavigationLink(destination: CommandsRootView()) {
                        Label("Commands", systemImage: "terminal")
                    }.tag("cli")
                }
            }
            .listStyle(.sidebar)
            .background(SplitViewAccessor(sideCollapsed: $collapsed))
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
                    Task { @MainActor in
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
                        model.presentCreateMachine = true
                    }) {
                        Label("New Machine", systemImage: "plus")
                    }.sheet(isPresented: $model.presentCreateMachine) {
                        CreateContainerView(isPresented: $model.presentCreateMachine, creatingCount: $model.creatingCount)
                    }
                    .help("New machine")
                }
            }

            ToolbarItem(placement: .automatic) {
                if model.state == .running && selection == "docker-volumes" {
                    Button(action: {
                        model.presentCreateVolume = true
                    }) {
                        Label("New Volume", systemImage: "plus")
                    }.sheet(isPresented: $model.presentCreateVolume) {
                        CreateVolumeView(isPresented: $model.presentCreateVolume)
                    }
                    .help("New Volume")
                }
            }

            ToolbarItem(placement: .automatic) {
                if model.state == .running && selection == "docker" {
                    Button(action: {
                        model.presentDockerFilter = true
                    }) {
                        Label("Filter", systemImage: "line.3.horizontal.decrease.circle")
                    }.popover(isPresented: $model.presentDockerFilter, arrowEdge: .bottom) {
                        DockerFilterView()
                    }
                    .help("Filter containers")
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
                NSLog("notification permission granted: \(granted)")
            }

            await model.initLaunch()
        }
        .onChange(of: controlActiveState) { state in
            if state == .key {
                Task {
                    NSLog("refresh: root view - key")
                    await model.tryRefreshList()
                }
            }
        }
        // error dialog
        .alert(isPresented: $presentError, error: model.error) { error in
            if error == VmError.killswitchExpired {
                Button("Update") {
                    NSWorkspace.shared.open(URL(string: "macvirt://update")!)
                }

                Button("Quit") {
                    NSApp.terminate(nil)
                }
            } else {
                Button("OK") {
                    model.dismissError()

                    // quit if the error is fatal
                    if model.state == .stopped && !model.reachedRunning {
                        NSApp.terminate(nil)
                    }
                }

                if error.shouldShowLogs {
                    Button("Report") {
                        model.dismissError()
                        openReportWindows()

                        // quit if the error is fatal
                        if model.state == .stopped && !model.reachedRunning {
                            NSApp.terminate(nil)
                        }
                    }
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
