import SwiftUI

struct DockerContainerFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let container: DKContainer

    var body: some View {
        FileManagerView(rootPath: container.nfsPath)
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenContainerInNewWindow {
                container.openFolder()
            }
        }
    }
}
