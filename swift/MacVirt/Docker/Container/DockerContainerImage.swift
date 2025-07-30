import SwiftUI

struct DockerContainerImage: View {
    @EnvironmentObject var vmModel: VmViewModel

    let container: DKContainer

    var body: some View {
        if let image = vmModel.dockerImages?[container.imageId] {
            DockerImageIcon(image: image)
        } else {
            DockerImageIconPlaceholder(id: container.imageId)
        }
    }
}
