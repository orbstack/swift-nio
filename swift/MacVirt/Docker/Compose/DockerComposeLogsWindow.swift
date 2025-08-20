import SwiftUI

struct DockerComposeLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var commandModel = CommandViewModel()

    // for hide sidebar workaround - unused
    @State private var collapsed = false
    // mirror from SceneStorage to fix flicker
    @State private var selection = "all"
    @State private var disabledChildren = Set<String>()
    @State private var isHoveringSection = false

    @SceneStorage("DockerComposeLogs_composeProject") private var composeProject: String?
    @SceneStorage("DockerComposeLogs_selection") private var savedSelection = "all"

    var body: some View {
        let children =
            vmModel.dockerContainers?.byComposeProject[composeProject]?.sorted {
                $0.userName < $1.userName
            } ?? []

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

                if let composeProject {
                    let projectLogArgs =
                        disabledChildren.isEmpty
                        ? []
                        :  // all
                        children
                            .map { $0.userName }
                            .filter { !disabledChildren.contains($0) }
                    let allDisabled = disabledChildren.count == children.count && !children.isEmpty

                    NavigationLink(tag: "all", selection: selBinding) {
                        DockerLogsContentView(
                            cid: .compose(project: composeProject),
                            standalone: false, extraComposeArgs: projectLogArgs,
                            allDisabled: allDisabled)
                    } label: {
                        Label("All", systemImage: "square.stack.3d.up")
                    }
                    .onAppear {
                        windowTracker.openDockerLogWindowIds.insert(
                            .compose(project: composeProject))
                    }
                    .onDisappear {
                        windowTracker.openDockerLogWindowIds.remove(
                            .compose(project: composeProject))
                    }

                    let bindings = children.map { container in
                        let serviceName = container.userName
                        return Binding<Bool>(
                            get: {
                                !disabledChildren.contains(serviceName)
                            },
                            set: {
                                if $0 {
                                    disabledChildren.remove(serviceName)
                                } else {
                                    disabledChildren.insert(serviceName)
                                }
                            })
                    }
                    Section {
                        ForEach(children, id: \.id) { container in
                            NavigationLink(tag: "container:\(container.id)", selection: selBinding)
                            {
                                DockerLogsContentView(cid: container.cid, standalone: false)
                            } label: {
                                let serviceName = container.userName
                                let enabledBinding = Binding<Bool>(
                                    get: {
                                        !disabledChildren.contains(serviceName)
                                    },
                                    set: {
                                        if $0 {
                                            disabledChildren.remove(serviceName)
                                        } else {
                                            disabledChildren.insert(serviceName)
                                        }
                                    })

                                HStack {
                                    Label {
                                        Text(serviceName)
                                    } icon: {
                                        // icon = red/green status dot
                                        Image(nsImage: SystemImages.statusDot(container.statusDot))
                                    }

                                    Spacer()

                                    Toggle(isOn: enabledBinding) {
                                        Text("Show in All")
                                    }
                                    .labelsHidden()
                                    .toggleStyle(.checkbox)
                                    .help("Show in All")
                                }
                                .contextMenu {
                                    Toggle(isOn: enabledBinding) {
                                        Text("Show in All")
                                    }
                                }
                            }
                        }
                    } header: {
                        HStack {
                            Text("Services")

                            Spacer()

                            // crude aproximation of macOS 15 .sectionActions
                            Toggle(sources: bindings, isOn: \.self) {
                                Text("Show All")
                            }
                            .toggleStyle(.checkbox)
                            .labelsHidden()
                            .help("Show All")
                            // lines up with checkboxes
                            .padding(.trailing, 14)
                            .opacity(isHoveringSection ? 1 : 0)
                        }
                        .frame(height: 28)
                    }
                    // no point in collapsing: you can just collapse the sidebar if you intend on only seeing All.
                    // arrow causes checkbox to shift around so this makes it easier
                    .collapsible(false)
                }
            }
            .onHover {
                isHoveringSection = $0
            }
            .listStyle(.sidebar)
            .background(SplitViewAccessor(sideCollapsed: $collapsed))

            ContentUnavailableViewCompat(
                "No Service Selected", systemImage: "questionmark.app.fill")
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            // check "base64" query param
            // for backward compat with restored state URLs, this is query-gated
            if url.query?.contains("base64") == true,
                let decoded = Data(base64URLEncoded: url.lastPathComponent)
            {
                composeProject = String(data: decoded, encoding: .utf8)
            } else {
                composeProject = url.lastPathComponent
            }
        }
        .onAppear {
            selection = savedSelection
        }
        .onChange(of: selection) { _, selection in
            savedSelection = selection
        }
        .ifLet(composeProject) { view, project in
            view.navigationTitle(WindowTitles.projectLogs(project))
        }
        .toolbar(forCommands: commandModel, hasSidebar: true)
        .searchable(text: $commandModel.searchField)
    }
}
