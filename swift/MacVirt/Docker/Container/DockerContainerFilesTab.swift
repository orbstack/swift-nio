import SwiftUI

struct DockerContainerFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let container: DKContainer

    var body: some View {
        if container.running {
            FileManagerView(rootPath: container.nfsPath)
                // render under toolbar
                .ignoresSafeArea()
                .onReceive(vmModel.toolbarActionRouter) { action in
                    if action == .dockerOpenContainerInNewWindow {
                        container.openFolder()
                    }
                }
        } else {
            ContentUnavailableViewCompat("Container Not Running", systemImage: "moon.zzz.fill") {
                Button {
                    Task {
                        await vmModel.tryDockerContainerStart(container.id)
                    }
                } label: {
                    Text("Start")
                        .padding(.horizontal, 4)
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .controlSize(.large)
            }
        }
    }
}
