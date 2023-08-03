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

    private var sidebarContents12: some View {
        Group {
            // on macOS 14, must put .tag() on Label or it crashes
            // on macOS <=13, must put .tag() on NavigationLink or it doesn't work
            Section(header: Text("Docker")) {
                NavigationLink(destination: DockerContainersRootView(initialSelection: initialDockerContainerSelection,
                                                                     selection: initialDockerContainerSelection, searchQuery: "")) {
                    Label("Containers", systemImage: "shippingbox")
                        .padding(.vertical, 3)
                }
                .tag("docker")
                
                NavigationLink(destination: DockerVolumesRootView()) {
                    Label("Volumes", systemImage: "externaldrive")
                        .padding(.vertical, 3)
                }
                .tag("docker-volumes")
                
                NavigationLink(destination: DockerImagesRootView()) {
                    Label("Images", systemImage: "doc.zipper")
                        .padding(.vertical, 3)
                }
                .tag("docker-images")
            }
            
            Section(header: Text("Linux")) {
                NavigationLink(destination: MachinesRootView()) {
                    Label("Machines", systemImage: "desktopcomputer")
                        .padding(.vertical, 3)
                }
                .tag("machines")
            }
            
            Section(header: Text("Help")) {
                NavigationLink(destination: CommandsRootView()) {
                    Label("Commands", systemImage: "terminal")
                        .padding(.vertical, 3)
                }
                .tag("cli")
            }
        }
    }
    
    var body: some View {
        Group {
            if #available(macOS 14, *) {
                // use NavigationSplitView on macOS 14 to fix tab switching crash
                // TODO: fix toggleSidebar button freezing for ~500 ms - that's why we don't use this on macOS 13
                NavigationSplitView {
                    List(selection: $selection) {
                        Section(header: Text("Docker")) {
                            NavigationLink(value: "docker") {
                                Label("Containers", systemImage: "shippingbox")
                                    .padding(.vertical, 3)
                            }
                            
                            NavigationLink(value: "docker-volumes") {
                                Label("Volumes", systemImage: "externaldrive")
                                    .padding(.vertical, 3)
                            }
                            
                            NavigationLink(value: "docker-images") {
                                Label("Images", systemImage: "doc.zipper")
                                    .padding(.vertical, 3)
                            }
                        }
                        
                        Section(header: Text("Linux")) {
                            NavigationLink(value: "machines") {
                                Label("Machines", systemImage: "desktopcomputer")
                                    .padding(.vertical, 3)
                            }
                        }
                        
                        Section(header: Text("Help")) {
                            NavigationLink(value: "cli") {
                                Label("Commands", systemImage: "terminal")
                                    .padding(.vertical, 3)
                            }
                        }
                    }
                    .listStyle(.sidebar)
                    .background(SplitViewAccessor(sideCollapsed: $collapsed))
                } detail: {
                    switch selection {
                    case "docker":
                        DockerContainersRootView(initialSelection: initialDockerContainerSelection, selection: initialDockerContainerSelection, searchQuery: "")
                    case "docker-volumes":
                        DockerVolumesRootView()
                    case "docker-images":
                        DockerImagesRootView()
                        
                    case "machines":
                        MachinesRootView()
                        
                    case "cli":
                        CommandsRootView()
                    
                    default:
                        Spacer()
                    }
                }
            } else {
                // binding helps us set default on <13
                let selBinding = Binding<String?>(get: {
                    selection
                }, set: {
                    if let sel = $0 {
                        selection = sel
                    }
                })

                NavigationView {
                    List(selection: selBinding) {
                        sidebarContents12
                    }
                    .listStyle(.sidebar)
                    .background(SplitViewAccessor(sideCollapsed: $collapsed))
                }
            }
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
        .toolbar {
            ToolbarItem(placement: .navigation) {
                // on macOS 14, NavigationSplitView provides this button and we can't disable it
                if #unavailable(macOS 14) {
                    Button(action: toggleSidebar, label: {
                        Label("Toggle Sidebar", systemImage: "sidebar.leading")
                    })
                    .help("Toggle Sidebar")
                }
            }

            ToolbarItem(placement: .automatic) {
                // conditional needs to be here because multiple .toolbar blocks doesn't work on macOS 12
                if selection == "machines" {
                    Button(action: {
                        model.presentCreateMachine = true
                    }) {
                        Label("New Machine", systemImage: "plus")
                    }
                    // careful: .keyboardShortcut after sheet composability applies to entire view (including Picker items) on macOS 12
                    .keyboardShortcut("n", modifiers: [.command])
                    .sheet(isPresented: $model.presentCreateMachine) {
                        CreateContainerView(isPresented: $model.presentCreateMachine, creatingCount: $model.creatingCount)
                    }
                    .help("New Machine")
                    .disabled(model.state != .running)
                }
            }

            ToolbarItem(placement: .automatic) {
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

            ToolbarItem(placement: .automatic) {
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

            // DO NOT use .task{} here.
            // start tasks should NOT be canceled
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
        .onDisappear {
            model.openMainWindowCount -= 1
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
            switch error {
            case VmError.killswitchExpired:
                Button("Update") {
                    NSWorkspace.shared.open(URL(string: "orbstack://update")!)
                }

                Button("Quit") {
                    model.terminateAppNow()
                }

            case VmError.wrongArch:
                Button("Download") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/download")!)
                }

                Button("Quit") {
                    model.terminateAppNow()
                }

            default:
                Button("OK") {
                    model.dismissError()

                    // quit if the error is fatal
                    if model.state == .stopped && !model.reachedRunning {
                        model.terminateAppNow()
                    }
                }

                if error.shouldShowLogs {
                    Button("Report") {
                        model.dismissError()
                        openBugReport()

                        // quit if the error is fatal
                        if model.state == .stopped && !model.reachedRunning {
                            model.terminateAppNow()
                        }
                    }
                }
            }
        } message: { error in
            if let msg = error.recoverySuggestion {
                Text(truncateError(description: msg))
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

func truncateError(description: String) -> String {
    if description.count > 2500 {
        return String(description.prefix(1250)) + "…" + String(description.suffix(1250))
    } else {
        return description
    }
}
