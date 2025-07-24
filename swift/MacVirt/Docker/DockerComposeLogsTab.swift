import SwiftUI

struct DockerComposeLogsTab: View {
    let project: String

    @StateObject private var commandModel = CommandViewModel()

    var body: some View {
        DockerLogsContentView(cid: .compose(project: project), standalone: true, extraComposeArgs: [], allDisabled: false)
        .environmentObject(commandModel)
    }
}
