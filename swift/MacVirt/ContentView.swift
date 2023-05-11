//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import UserNotifications
import Sparkle

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

    @StateObject private var actionTracker = ActionTracker()

    // SceneStorage inits too late
    @AppStorage("root.selectedTab") private var selection = "docker"
    @AppStorage("onboardingCompleted") private var onboardingCompleted = false
    @State private var startStopInProgress = false
    @State private var presentError = false
    @State private var pendingClose = false
    @State private var collapsed = false
    // with searchable, this breaks if it's on model, but works as state
    @State private var presentDockerFilter = false

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

                    NavigationLink(destination: DockerImagesRootView()) {
                        Label("Images", systemImage: "doc.zipper")
                    }.tag("docker-images")
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
            .toolbarMacOS13(id: "sidebar-toolbar") {
                ToolbarItem(id: "ctl-power", placement: .automatic) {
                    Button(action: {
                        Task { @MainActor in
                            self.startStopInProgress = true
                            if model.state == .running {
                                await model.tryStop()
                            } else {
                                await model.tryStartAndWait()
                            }
                            self.startStopInProgress = false
                        }
                    }) {
                        Label(model.state == .running ? "Stop" : "Start", systemImage: "power")
                    }
                    .disabled(startStopInProgress)
                    .help(model.state == .running ? "Stop everything" : "Start everything")
                }
            }
        }
        .environmentObject(actionTracker)
        .toolbar(id: "main-toolbar") {
            ToolbarItem(id: "toggle-sidebar", placement: .navigation) {
                Button(action: toggleSidebar, label: {
                    Label("Toggle Sidebar", systemImage: "sidebar.leading")
                })
                .help("Toggle Sidebar")
            }

            ToolbarItem(id: "macos12-power", placement: .automatic) {
                if #available(macOS 13.0, *) {
                    // in sidebar instead
                } else {
                    Button(action: {
                        Task { @MainActor in
                            self.startStopInProgress = true
                            if model.state == .running {
                                await model.tryStop()
                            } else {
                                await model.tryStartAndWait()
                            }
                            self.startStopInProgress = false
                        }
                    }) {
                        Label(model.state == .running ? "Stop" : "Start", systemImage: "power")
                    }
                            .disabled(startStopInProgress)
                            .help(model.state == .running ? "Stop everything" : "Start everything")
                }
            }
            
            ToolbarItem(id: "machines-new", placement: .automatic) {
                if selection == "machines" {
                    Button(action: {
                        model.presentCreateMachine = true
                    }) {
                        Label("New Machine", systemImage: "plus")
                    }
                    // careful: .keyboardShortcut after sheet composability applies to entire CreateContainerView (including Picker items) on macOS 12
                    .keyboardShortcut("n", modifiers: [.command])
                    .sheet(isPresented: $model.presentCreateMachine) {
                        CreateContainerView(isPresented: $model.presentCreateMachine, creatingCount: $model.creatingCount)
                    }
                    .help("New Machine")
                    .disabled(model.state != .running)
                }
            }

            ToolbarItem(id: "docker-volume-new", placement: .automatic) {
                if selection == "docker-volumes" {
                    Button(action: {
                        model.presentCreateVolume = true
                    }) {
                        Label("New Volume", systemImage: "plus")
                    }
                    .help("New Volume")
                    .disabled(model.state != .running)
                    .keyboardShortcut("n", modifiers: [.command])
                }
            }

            ToolbarItem(id: "docker-container-filter", placement: .automatic) {
                if selection == "docker" {
                    Button(action: {
                        presentDockerFilter = true
                    }) {
                        Label("Filter", systemImage: "line.3.horizontal.decrease.circle")
                    }.popover(isPresented: $presentDockerFilter, arrowEdge: .bottom) {
                        DockerFilterView()
                    }
                    .help("Filter Containers")
                    .disabled(model.state != .running)
                }
            }
        }
        .onAppear {
            if !onboardingCompleted {
                pendingClose = true
                NSWorkspace.shared.open(URL(string: "orbstack://onboarding")!)
            }
        }
        .task {
            let center = UNUserNotificationCenter.current()
            do {
                let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
                NSLog("notification request granted: \(granted)")
            } catch {
                NSLog("notification request failed: \(error)")
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
                    NSWorkspace.shared.open(URL(string: "orbstack://update")!)
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
        .onReceive(model.$error, perform: { error in
            presentError = error != nil

            if error == VmError.killswitchExpired {
                // trigger updater as well
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    NSWorkspace.shared.open(URL(string: "orbstack://update")!)
                }
            }
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

extension View {
    func toolbarMacOS13<Content: CustomizableToolbarContent>(id: String, @ToolbarContentBuilder content: () -> Content) -> some View {
        if #available(macOS 13.0, *) {
            return self.toolbar(id: id, content: content)
        } else {
            return self
        }
    }
}