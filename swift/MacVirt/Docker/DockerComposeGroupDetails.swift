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
            let containers = vmModel.dockerContainers?.byComposeProject[project] ?? []

            DetailsListSection("Containers in Group") {
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

            DetailsButtonSection {
                DetailsButton {
                    ComposeGroup(project: project).showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                if let projectPath = containers.first?.composeConfigFiles?.first {
                    DetailsButton {
                        let parentDir = URL(fileURLWithPath: projectPath)
                            .deletingLastPathComponent().path
                        NSWorkspace.shared.selectFile(
                            projectPath, inFileViewerRootedAtPath: parentDir)
                    } label: {
                        Label("Show in Finder", systemImage: "folder")
                    }
                }
            }
        }
    }
}
