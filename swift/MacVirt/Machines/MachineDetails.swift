//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct MachineDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var actionTracker: ActionTracker
    @StateObject private var windowHolder = WindowHolder()

    let info: ContainerInfo

    var body: some View {
        DetailsStack {
            DetailsSection("Info") {
                // match Image section
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Name") {
                        CopyableText(info.record.name)
                    }

                    SimpleKvTableRow("Status") {
                        Text(info.record.state.friendlyName)
                    }

                    let domain = "\(info.record.name).orb.local"
                    if let url = URL(string: "http://\(domain)") {
                        SimpleKvTableRow("Domain") {
                            if info.record.running && vmModel.netBridgeAvailable {
                                CopyableText(copyAs: domain) {
                                    CustomLink(domain, url: url)
                                }
                            } else {
                                CopyableText(domain)
                            }
                        }
                    }
                }
            }

            DetailsSection("Image") {
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("Distro") {
                        Text(
                            Distro.map[info.record.image.distro]?.friendlyName
                                ?? info.record.image.distro)
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

            if let diskSize = info.diskSize {
                DetailsSection("Resources") {
                    SimpleKvTable(longestLabel: "Disk usage") {
                        SimpleKvTableRow("Disk usage") {
                            Text(diskSize.formatted(.byteCount(style: .file)))
                        }
                    }
                }
            }

            DividedButtonStack {
                DividedRowButton {
                    info.record.openNfsDirectory()
                } label: {
                    Label("Files", systemImage: "folder")
                }

                DividedRowButton {
                    Task {
                        await info.record.openInTerminal()
                    }
                } label: {
                    Label("Terminal", systemImage: "terminal")
                }

                DividedRowButton {
                    info.record.openExportPanel(
                        windowHolder: windowHolder,
                        actionTracker: actionTracker,
                        vmModel: vmModel
                    )
                } label: {
                    Label("Export", systemImage: "square.and.arrow.up")
                }
            }
        }
        .windowHolder(windowHolder)
    }
}
