import SwiftUI

struct DockerImageFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let image: DKSummaryAndFullImage

    var body: some View {
        if image.summary.hasTag {
            FileManagerView(rootPath: image.summary.nfsPath, readOnly: true)
                // render under toolbar
                .ignoresSafeArea()
                .onReceive(vmModel.toolbarActionRouter) { action in
                    if action == .dockerOpenImageInNewWindow {
                        image.summary.openFolder()
                    }
                }
        } else {
            ContentUnavailableViewCompat(
                "Files Not Available", systemImage: "tag.slash",
                desc: "Only tagged images are supported.")
        }
    }
}
