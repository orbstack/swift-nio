import SwiftUI

struct DockerContainerLogsTab: View {
    let container: DKContainer

    var body: some View {
        LogsTabToolbarWrapper {
            DockerLogsContentView(
                cid: container.cid, standalone: true, extraComposeArgs: [], allDisabled: false
            )
            // render under toolbar
            .ignoresSafeArea()
        }
    }
}
