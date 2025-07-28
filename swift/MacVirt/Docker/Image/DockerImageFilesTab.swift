import SwiftUI

struct DockerImageFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let image: DKSummaryAndFullImage

    var body: some View {
        FileManagerView(rootPath: image.summary.nfsPath)
            .onReceive(vmModel.toolbarActionRouter) { action in
                if action == .dockerOpenImageInNewWindow {
                    image.summary.openFolder()
                }
            }
    }
}
