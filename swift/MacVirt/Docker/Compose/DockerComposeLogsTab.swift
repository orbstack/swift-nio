import SwiftUI

struct DockerComposeLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    let project: String

    @StateObject private var commandModel = CommandViewModel()

    var body: some View {
        DockerLogsContentView(
            cid: .compose(project: project), standalone: true, extraComposeArgs: [],
            allDisabled: false
        )
        // render under toolbar
        .ignoresSafeArea()
        .environmentObject(commandModel)
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenContainerInNewWindow {
                ComposeGroup(project: project).showLogs(windowTracker: windowTracker)
            }
        }
    }
}
