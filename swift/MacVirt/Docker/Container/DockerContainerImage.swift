import SwiftUI

struct DockerContainerImage: View {
    let container: DKContainer

    var body: some View {
        DockerImageIcon(rawImageTags: [container.image])
    }
}
