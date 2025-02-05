//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct MachineDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let info: ContainerInfo

    var body: some View {
        DetailsStack {
            DetailsSection("Info") {
                // match Image section
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Status") {
                        Text(info.record.state.friendlyName)
                    }
                    SimpleKvTableRow("Domain") {
                        CopyableText("\(info.record.name).orb.local")
                            .lineLimit(nil)
                    }
                }
            }

            DetailsSection("Image") {
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Distro") {
                        Text(
                            Distro.allCases.first(where: { $0.rawValue == info.record.image.distro }
                            )?
                            .friendlyName ?? info.record.image.distro)
                    }
                    SimpleKvTableRow("Version") {
                        Text(info.record.image.version)
                    }
                    SimpleKvTableRow("Architecture") {
                        Text(info.record.image.arch)
                    }
                }
            }

            DetailsSection("Settings") {
                // match Image section
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Username") {
                        Text(info.record.config.defaultUsername ?? Files.username)
                    }
                }
            }
        }
    }
}
