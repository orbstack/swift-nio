import SwiftUI

struct DockerVolumeFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let volume: DKVolume

    var body: some View {
        FileManagerView(rootPath: volume.nfsPath)
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenVolumeInNewWindow {
                volume.openNfsDirectory()
            }
        }
    }
}
