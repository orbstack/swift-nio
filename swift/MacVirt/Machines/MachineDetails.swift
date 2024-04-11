//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct MachineDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let record: ContainerRecord

    var body: some View {
        DetailsStack {
            DetailsSection("Info") {
                // match Image section
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Status") {
                        Text(record.state.friendlyName)
                    }
                    SimpleKvTableRow("Domain") {
                        CopyableText("\(record.name).orb.local")
                        .lineLimit(nil)
                    }
                }
            }

            DetailsSection("Image") {
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Distro") {
                        Text(Distro.allCases.first(where: { $0.rawValue == record.image.distro })?.friendlyName ?? record.image.distro)
                    }
                    SimpleKvTableRow("Version") {
                        Text(record.image.version)
                    }
                    SimpleKvTableRow("Architecture") {
                        Text(record.image.arch)
                    }
                }
            }

            DetailsSection("Settings") {
                // match Image section
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Username") {
                        Text(record.config.defaultUsername ?? Files.username)
                    }
                }
            }
        }
    }
}
