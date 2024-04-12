//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerComposeGroupDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let project: String

    var body: some View {
        DetailsStack {
            let containers = vmModel.dockerContainers?
                .filter { $0.composeProject == project } ?? []

            DetailsSection("Containers in Group") {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(containers) { container in
                        // TODO: link
                        Label {
                            CopyableText(container.userName)
                        } icon: {
                            // icon = red/green status dot
                            Image(nsImage: SystemImages.statusDot(container.statusDot))
                        }
                    }
                }
            }

            if let projectPath = containers.first?.composeConfigFiles?.first {
                DividedButtonStack {
                    DividedRowButton {
                        let parentDir = URL(fileURLWithPath: projectPath).deletingLastPathComponent().path
                        NSWorkspace.shared.selectFile(projectPath, inFileViewerRootedAtPath: parentDir)
                    } label: {
                        Label("Show in Finder", systemImage: "folder")
                    }
                }
            }
        }
    }
}
