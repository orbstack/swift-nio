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

private struct NavTab: View {
    private let label: String
    private let systemImage: String

    init(_ label: String, systemImage: String) {
        self.label = label
        self.systemImage = systemImage
    }

    var body: some View {
        Label(label, systemImage: systemImage)
        .padding(.vertical, 6)
        .padding(.horizontal, 4)
    }
}

struct MainWindow: View {
    @Environment(\.controlActiveState) var controlActiveState
    @EnvironmentObject private var model: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    // SceneStorage inits too late
    @Default(.selectedTab) private var selection
    @Default(.onboardingCompleted) private var onboardingCompleted
    @State private var presentError = false
    @State private var pendingClose = false
    @State private var collapsed = false
    // with searchable, this breaks if it's on model, but works as state
    @State private var presentDockerFilter = false
    @State private var presentK8sFilter = false
    @State private var presentAuth = false

    @State private var initialDockerContainerSelection: Set<DockerContainerId> = []

    @ViewBuilder
    private var sidebarContents12: some View {
        List {
            // List(selection:) should NOT be used for navigation: https://kean.blog/post/triple-trouble
            // NavigationLink(tag:selection:) expects Binding<String?> so make a binding to ignore nil
            let selBinding = Binding<String?>(get: {
                selection
            }, set: {
                if let sel = $0 {
                    selection = sel
                }
            })

            // on macOS 14, must put .tag() on Label or it crashes
            // on macOS <=13, must put .tag() on NavigationLink or it doesn't work
            Section(header: Text("Docker")) {
                NavigationLink(tag: "docker", selection: selBinding) {
                    DockerContainersRootView(selection: initialDockerContainerSelection, searchQuery: "")
                } label: {
                    NavTab("Containers", systemImage: "shippingbox")
                }
                
                NavigationLink(tag: "docker-volumes", selection: selBinding) {
                    DockerVolumesRootView()
                } label: {
                    NavTab("Volumes", systemImage: "externaldrive")
                }
                
                NavigationLink(tag: "docker-images", selection: selBinding) {
                    DockerImagesRootView()
                } label: {
                    NavTab("Images", systemImage: "doc.zipper")
                }
            }
            .tag("docker")

            Section(header: Text("Kubernetes")) {
                NavigationLink(tag: "k8s-pods", selection: selBinding) {
                    K8SPodsView()
                } label: {
                    NavTab("Pods", systemImage: "helm")
                }

                NavigationLink(tag: "k8s-services", selection: selBinding) {
                    K8SServicesView()
                } label: {
                    NavTab("Services", systemImage: "network")
                }
            }
            
            Section(header: Text("Linux")) {
                NavigationLink(tag: "machines", selection: selBinding) {
                    MachinesRootView()
                } label: {
                    NavTab("Machines", systemImage: "desktopcomputer")
                }
            }
            
            Section(header: Text("Help")) {
                NavigationLink(tag: "cli", selection: selBinding) {
                    CommandsRootView()
                } label: {
                    NavTab("Commands", systemImage: "terminal")
                }
            }
        }
        .listStyle(.sidebar)
        .background(SplitViewAccessor(sideCollapsed: $collapsed))
        // "Personal use only" subheadline
        .frame(minWidth: 160, maxWidth: 500)
        .safeAreaInset(edge: .bottom, alignment: .leading, spacing: 0) {
            VStack {
                Button {
                    if model.drmState.refreshToken != nil {
                        // manage account
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
                    } else {
                        presentAuth = true
                    }
                } label: {
                    HStack {
                        //TODO load and cache avatar image
                        var drmState = model.drmState

                        Image(systemName: "person.circle")
                        .resizable()
                        .frame(width: 24, height: 24)
                        .foregroundColor(.accentColor)
                        .padding(.trailing, 2)

                        VStack(alignment: .leading) {
                            Text(drmState.title ?? "Sign in")
                            .font(.headline)

                            Text(drmState.subtitle ?? "Personal use only")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                        }
                    }
                    .padding(16)
                    // occupy full rect
                    .onRawDoubleClick { }
                }
                .buttonStyle(.plain)
                .contextMenu {
                    Button("Manage…") {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
                    }

                    Button("Switch Organization…") {
                        // simple: just reauth and use web org picker
                        presentAuth = true
                    }

                    Divider()

                    Button("Sign Out") {
                        Task { @MainActor in
                            await model.trySignOut()
                        }
                    }
                }
            }
            .border(width: 1, edges: [.top], color: Color(NSColor.separatorColor).opacity(0.5))
        }
        .sheet(isPresented: $presentAuth) {
            AuthView(sheetPresented: $presentAuth)
        }
    }

    @available(macOS 14, *)
    private var sidebarContents14: some View {
        List(selection: $selection) {
            Section(header: Text("Docker")) {
                NavigationLink(value: "docker") {
                    NavTab("Containers", systemImage: "shippingbox")
                }

                NavigationLink(value: "docker-volumes") {
                    NavTab("Volumes", systemImage: "externaldrive")
                }

                NavigationLink(value: "docker-images") {
                    NavTab("Images", systemImage: "doc.zipper")
                }
            }

            Section(header: Text("Kubernetes")) {
                NavigationLink(value: "k8s-pods") {
                    NavTab("Pods", systemImage: "helm")
                }

                NavigationLink(value: "k8s-services") {
                    NavTab("Services", systemImage: "network")
                }
            }

            Section(header: Text("Linux")) {
                NavigationLink(value: "machines") {
                    NavTab("Machines", systemImage: "desktopcomputer")
                }
            }

            Section(header: Text("Help")) {
                NavigationLink(value: "cli") {
                    NavTab("Commands", systemImage: "terminal")
                }
            }
        }
        .listStyle(.sidebar)
        .background(SplitViewAccessor(sideCollapsed: $collapsed))
    }

    var body: some View {
        Group {
            if #available(macOS 14, *) {
                // use NavigationSplitView on macOS 14 to fix tab switching crash
                // TODO: fix toggleSidebar button freezing for ~500 ms - that's why we don't use this on macOS 13
                NavigationSplitView {
                    sidebarContents14
                } detail: {
                    switch selection {
                    case "docker":
                        DockerContainersRootView(selection: initialDockerContainerSelection, searchQuery: "")
                    case "docker-volumes":
                        DockerVolumesRootView()
                    case "docker-images":
                        DockerImagesRootView()

                    case "k8s-pods":
                        K8SPodsView()
                    case "k8s-services":
                        K8SServicesView()
                        
                    case "machines":
                        MachinesRootView()
                        
                    case "cli":
                        CommandsRootView()
                    
                    default:
                        Spacer()
                    }
                }
            } else {
                NavigationView {
                    sidebarContents12

                    ContentUnavailableViewCompat("No Tab Selected", systemImage: "questionmark.app.fill")
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
                    ToggleSidebarButton()
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
                        CreateContainerView(isPresented: $model.presentCreateMachine)
                    }
                    .help("New Machine")
                    .disabled(model.state != .running)
                }
            }

            ToolbarItem(placement: .automatic) {
                if selection == "docker-images" {
                    Button(action: {
                        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: Folders.nfsDockerImages)
                    }) {
                        Label("Open Images", systemImage: "folder")
                    }
                    .help("Open Images")
                    .disabled(model.state != .running)
                    .keyboardShortcut("o", modifiers: [.command])
                }
            }

            ToolbarItem(placement: .automatic) {
                if selection == "docker-volumes" {
                    Button(action: {
                        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: Folders.nfsDockerVolumes)
                    }) {
                        Label("Open Volumes", systemImage: "folder")
                    }
                    .help("Open Volumes")
                    .disabled(model.state != .running)
                    .keyboardShortcut("o", modifiers: [.command])
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

            ToolbarItem(placement: .automatic) {
                if selection == "k8s-pods" && model.config != nil {
                    let binding = Binding(
                            get: { model.config?.k8sEnable ?? true },
                            set: { newValue in
                                Task { @MainActor in
                                    await model.tryStartStopK8s(enable: newValue)
                                }
                            }
                    )

                    Toggle("Enable Kubernetes", isOn: binding)
                    .toggleStyle(.switch)
                    .help("Enable Kubernetes")
                }
            }

            ToolbarItem(placement: .automatic) {
                if selection == "k8s-pods" || selection == "k8s-services" {
                    Button(action: {
                        presentK8sFilter = true
                    }) {
                        Label("Filter", systemImage: "line.3.horizontal.decrease.circle")
                    }.popover(isPresented: $presentK8sFilter, arrowEdge: .bottom) {
                        K8SFilterView()
                    }
                    .help("Filter")
                    .disabled(model.state != .running)
                }
            }

            ToolbarItem(placement: .automatic) {
                if selection == "cli" {
                    Button(action: {
                        NSWorkspace.shared.open(URL(string: "https://go.orbstack.dev/cli")!)
                    }) {
                        Label("Go to Docs", systemImage: "questionmark.circle")
                    }
                    .help("Go to Docs")
                }
            }
        }
        .onAppear {
            windowTracker.openMainWindowCount += 1
            model.initLaunch()

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
            }
        }
        .onDisappear {
            windowTracker.openMainWindowCount -= 1
        }
        // error dialog
        .alert(isPresented: $presentError, error: model.error) { error in
            switch error {
            case VmError.killswitchExpired:
                Button("Update") {
                    NSWorkspace.openSubwindow("update")
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
                if model.state == .stopped && !model.reachedRunning {
                    Button("Quit") {
                        model.terminateAppNow()
                    }
                } else {
                    Button("OK") {
                        model.dismissError()
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
                    NSWorkspace.openSubwindow("update")
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
