//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import UserNotifications
import Sparkle
import Defaults

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
    @Default(.selectedTab) private var selection
    @Default(.onboardingCompleted) private var onboardingCompleted
    @State private var presentError = false
    @State private var pendingClose = false
    @State private var collapsed = false
    // with searchable, this breaks if it's on model, but works as state
    @State private var presentDockerFilter = false

    @State private var initialDockerContainerSelection: Set<DockerContainerId> = []

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
                    NavigationLink(destination: DockerContainersRootView(initialSelection: initialDockerContainerSelection, selection: initialDockerContainerSelection, searchQuery: "")) {
                        Label("Containers", systemImage: "shippingbox")
                                .padding(.vertical, 3)
                    }.tag("docker")

                    NavigationLink(destination: DockerVolumesRootView()) {
                        Label("Volumes", systemImage: "externaldrive")
                                .padding(.vertical, 3)
                    }.tag("docker-volumes")

                    NavigationLink(destination: DockerImagesRootView()) {
                        Label("Images", systemImage: "doc.zipper")
                                .padding(.vertical, 3)
                    }.tag("docker-images")
                }

                Section(header: Text("Linux")) {
                    NavigationLink(destination: MachinesRootView()) {
                        Label("Machines", systemImage: "desktopcomputer")
                                .padding(.vertical, 3)
                    }.tag("machines")

                    NavigationLink(destination: FilesRootView()) {
                        Label("Files", systemImage: "folder")
                                .padding(.vertical, 3)
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
        .onOpenURL { url in
            // for menu bar
            // TODO unstable
            if url.pathComponents.count >= 2,
               url.pathComponents[1] == "containers" || url.pathComponents[1] == "projects" {
                initialDockerContainerSelection = [.container(id: url.pathComponents[2])]
                selection = "docker"
            }
        }
        .toolbar(id: "main-toolbar") {
            ToolbarItem(id: "toggle-sidebar", placement: .navigation) {
                Button(action: toggleSidebar, label: {
                    Label("Toggle Sidebar", systemImage: "sidebar.leading")
                })
                        .help("Toggle Sidebar")
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
            model.openMainWindowCount += 1

            if !onboardingCompleted {
                pendingClose = true
                NSWorkspace.shared.open(URL(string: "orbstack://onboarding")!)
            }
        }
        .onDisappear {
            model.openMainWindowCount -= 1
        }
        // DO NOT use .task{} here.
        // start tasks should NOT be canceled
        .onAppear {
            Task { @MainActor in
                let center = UNUserNotificationCenter.current()
                do {
                    let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
                    NSLog("notification request granted: \(granted)")
                } catch {
                    NSLog("notification request failed: \(error)")
                }

                await model.initLaunch()
            }
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