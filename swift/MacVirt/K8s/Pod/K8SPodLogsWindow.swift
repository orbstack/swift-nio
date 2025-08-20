import SwiftUI

struct K8SLogsContentView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var commandModel: CommandViewModel
    @StateObject private var model = LogsViewModel()

    let kid: K8SResourceId
    let containerName: String?

    var body: some View {
        K8SStateWrapperView(\.k8sPods) { pods, _ in
            if case let .pod(namespace, name) = kid,
                pods.contains(where: { $0.id == kid })
            {
                LogsView(
                    cmdExe: AppConfig.kubectlExe,
                    args: [
                        "logs", "--context", K8sConstants.context, "-n", namespace, "pod/\(name)",
                        "-f",
                    ],
                    extraArgs: containerName == nil
                        ? ["--all-containers=true"] : ["-c", containerName!],
                    extraState: [],
                    model: model
                )
                .navigationTitle(WindowTitles.podLogs(name))
            } else {
                ContentUnavailableViewCompat(
                    "Pod Removed", systemImage: "trash", desc: "No logs available.")
            }
        }
        .onAppear {
            // TODO: why doesn't for-await + .task() work? (that way we get auto-cancel)
            model.monitorCommands(commandModel: commandModel)
            // TODO: equivalent of monitorContainers for pod recreate? or unlikely b/c of deployment + random names
        }
        .frame(minWidth: 400, minHeight: 200)
    }
}

struct K8SPodLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var commandModel = CommandViewModel()

    // for hide sidebar workaround - unused
    @State private var collapsed = false
    // mirror from SceneStorage to fix flicker
    @State private var selection = "all"

    @SceneStorage("K8SLogs_namespaceAndName") private var namespaceAndName: String?
    @SceneStorage("K8SLogs_selection") private var savedSelection = "all"

    var body: some View {
        NavigationView {
            List {
                let selBinding = Binding<String?>(
                    get: {
                        selection
                    },
                    set: {
                        if let sel = $0 {
                            selection = sel
                        }
                    })

                if let namespaceAndName,
                    let kid = K8SResourceId.podFromNamespaceAndName(namespaceAndName)
                {
                    let children =
                        vmModel.k8sPods?.first { $0.id == kid }?.status.containerStatuses?.sorted {
                            ($0.name ?? "") < ($1.name ?? "")
                        } ?? []

                    NavigationLink(tag: "all", selection: selBinding) {
                        K8SLogsContentView(kid: kid, containerName: nil)
                    } label: {
                        Label("All", systemImage: "square.stack.3d.up")
                    }
                    .onAppear {
                        windowTracker.openK8sLogWindowIds.insert(kid)
                    }
                    .onDisappear {
                        windowTracker.openK8sLogWindowIds.remove(kid)
                    }

                    Section("Containers") {
                        ForEach(children, id: \.name) { container in
                            let k8sContainerName = container.name ?? "<unknown>"
                            NavigationLink(
                                tag: "container:\(k8sContainerName)", selection: selBinding
                            ) {
                                K8SLogsContentView(kid: kid, containerName: k8sContainerName)
                            } label: {
                                Label {
                                    Text(k8sContainerName)
                                } icon: {
                                    // icon = red/green status dot
                                    Image(
                                        nsImage: SystemImages.statusDot(
                                            isRunning: container.ready ?? false))
                                }
                            }
                        }
                    }
                }
            }
            .listStyle(.sidebar)
            .background(SplitViewAccessor(sideCollapsed: $collapsed))

            ContentUnavailableViewCompat(
                "No Service Selected", systemImage: "questionmark.app.fill")
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            if let decoded = Data(base64URLEncoded: url.lastPathComponent),
                let namespaceAndName = String(data: decoded, encoding: .utf8)
            {
                self.namespaceAndName = namespaceAndName
            }
        }
        .onAppear {
            selection = savedSelection
        }
        .onChange(of: selection) { _, selection in
            savedSelection = selection
        }
        .toolbar(forCommands: commandModel, hasSidebar: true)
        .searchable(text: $commandModel.searchField)
    }
}
