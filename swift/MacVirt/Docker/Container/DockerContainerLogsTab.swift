import SwiftUI

struct DockerContainerLogsTab: View {
    let container: DKContainer

    @StateObject private var commandModel = CommandViewModel()

    var body: some View {
        DockerLogsContentView(
            cid: container.cid, standalone: true, extraComposeArgs: [], allDisabled: false
        )
        // render under toolbar
        .ignoresSafeArea()
        .environmentObject(commandModel)
    }
}
