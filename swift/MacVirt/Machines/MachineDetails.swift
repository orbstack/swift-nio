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
                SimpleKvTable {
                    SimpleKvTableRow("Status") {
                        Text(record.state.friendlyName)
                    }
                    SimpleKvTableRow("Address") {
                        CopyableText("\(record.name).orb.local")
                    }

                    // for hacky alignment purposes, to match "Image" section...
                    HStack(alignment: .top) {
                        Text("Architecture")
                            .fontWeight(.medium)
                            .alignmentGuide(.tableColumnAlignmentGuide) { context in
                                context[.trailing]
                            }

                        EmptyView()
                    }
                    .frame(maxHeight: 1)
                    .opacity(0)
                    .accessibility(hidden: true)
                }
            }

            DetailsSection("Image") {
                SimpleKvTable {
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
                SimpleKvTable {
                    SimpleKvTableRow("Username") {
                        Text(record.config.defaultUsername ?? Files.username)
                    }

                    // for hacky alignment purposes, to match "Image" section...
                    HStack(alignment: .top) {
                        Text("Architecture")
                            .fontWeight(.medium)
                            .alignmentGuide(.tableColumnAlignmentGuide) { context in
                                context[.trailing]
                            }

                        EmptyView()
                    }
                    .frame(maxHeight: 1)
                    .opacity(0)
                    .accessibility(hidden: true)
                }
            }
        }
    }
}
