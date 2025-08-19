import SwiftUI

struct DockerComposeLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    let project: String

    var body: some View {
        LogsTabToolbarWrapper {
            DockerLogsContentView(
                cid: .compose(project: project), standalone: true, extraComposeArgs: [],
                allDisabled: false
            )
            // render under toolbar
            .ignoresSafeArea()
            .onReceive(vmModel.toolbarActionRouter) { action in
                if action == .dockerOpenContainerInNewWindow {
                    ComposeGroup(project: project).showLogs(windowTracker: windowTracker)
                }
            }
        }
    }
}
