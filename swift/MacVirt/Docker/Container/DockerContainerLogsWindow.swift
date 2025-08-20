import SwiftUI

struct DockerLogsContentView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var commandModel: CommandViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    @StateObject private var model = LogsViewModel()

    let cid: DockerContainerId
    // individual container, not compose
    let standalone: Bool
    let extraComposeArgs: [String]
    let allDisabled: Bool

    init(
        cid: DockerContainerId, standalone: Bool, extraComposeArgs: [String] = [],
        allDisabled: Bool = false
    ) {
        self.cid = cid
        self.standalone = standalone
        self.extraComposeArgs = extraComposeArgs
        self.allDisabled = allDisabled
    }

    var body: some View {
        DockerStateWrapperView(\.dockerContainers) { containers, _ in
            if allDisabled {
                ContentUnavailableViewCompat("No Containers Selected", systemImage: "moon.zzz")
            } else if case let .container(containerId) = cid,
                let container = containers.byId[containerId]
            {
                LogsView(
                    cmdExe: AppConfig.dockerExe,
                    args: ["logs", "-f", "-n", String(logsMaxLines), containerId],
                    extraArgs: [],
                    // trigger restart on start/stop state change
                    // don't trigger on starting/stopping/deleting/...
                    extraState: [container.state == "running" ? "running" : "not_running"],
                    model: model
                )
                .if(standalone) {
                    $0.navigationTitle(WindowTitles.containerLogs(container.userName))
                }
                .onAppear {
                    // save name so we can keep going after container is recreated
                    model.lastContainerName = container.names.first
                }
                .onReceive(vmModel.toolbarActionRouter) { action in
                    if action == .dockerOpenContainerInNewWindow {
                        container.showLogs(windowTracker: windowTracker)
                    }
                }
            } else if let containerName = model.lastContainerName,
                let container = vmModel.dockerContainers?.byName[containerName]
            {
                // if restarted, use name
                // don't update id - it'll cause unnecessary logs restart
                LogsView(
                    cmdExe: AppConfig.dockerExe,
                    args: ["logs", "-f", "-n", String(logsMaxLines), container.id],
                    extraArgs: [],
                    extraState: [],
                    model: model
                )
                .if(standalone) {
                    $0.navigationTitle(WindowTitles.containerLogs(container.userName))
                }
            } else if case let .compose(composeProject) = cid {
                LogsView(
                    cmdExe: AppConfig.dockerComposeExe,
                    args: ["-p", composeProject, "logs", "-f", "-n", String(logsMaxLines)],
                    extraArgs: extraComposeArgs,
                    extraState: [],
                    model: model)
            } else {
                ContentUnavailableViewCompat(
                    "Container Removed", systemImage: "trash", desc: "No logs available.")
            }
        }
        .onAppear {
            // TODO: why doesn't for-await + .task() work? (that way we get auto-cancel)
            model.monitorCommands(commandModel: commandModel)
            model.monitorContainers(vmModel: vmModel, cid: cid)
        }
        .frame(minWidth: 400, minHeight: 200)
    }
}

struct DockerContainerLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @StateObject private var commandModel = CommandViewModel()

    @SceneStorage("DockerLogs_containerId") private var containerId: String?

    var body: some View {
        Group {
            if let containerId {
                DockerLogsContentView(cid: .container(id: containerId), standalone: true)
                    .onAppear {
                        windowTracker.openDockerLogWindowIds.insert(.container(id: containerId))
                    }
                    .onDisappear {
                        windowTracker.openDockerLogWindowIds.remove(.container(id: containerId))
                    }
            }
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            containerId = url.lastPathComponent
        }
        .toolbar(forCommands: commandModel)
        .searchable(text: $commandModel.searchField)
    }
}
