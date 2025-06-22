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
            DetailsKvSection {
                DetailsRow("Name", copyableText: info.record.name)
                DetailsRow("Status", text: info.record.state.friendlyName)

                let domain = "\(info.record.name).orb.local"
                if let url = URL(string: "http://\(domain)") {
                    DetailsRow("Domain") {
                        if info.record.running && vmModel.netBridgeAvailable {
                            CopyableText(copyAs: domain) {
                                CustomLink(domain, url: url)
                            }
                        } else {
                            CopyableText(domain)
                        }
                    }
                }

                if let ip4 = info.ip4 {
                    DetailsRow("IP", copyableText: ip4)
                }
            }

            DetailsKvSection("Image") {
                DetailsRow("Distro", text: Distro.map[info.record.image.distro]?.friendlyName ?? info.record.image.distro)
                DetailsRow("Version", text: info.record.image.version)
                DetailsRow("Architecture", text: info.record.image.arch)
            }

            DetailsKvSection("Settings") {
                DetailsRow("Username", text: info.record.config.defaultUsername ?? Files.username)
            }

            if let diskSize = info.diskSize {
                DetailsKvSection("Resources") {
                    DetailsRow("Disk usage", text: diskSize.formatted(.byteCount(style: .file)))
                }
            }

            DetailsButtonSection {
                DetailsButton {
                    info.record.openNfsDirectory()
                } label: {
                    Label("Files", systemImage: "folder")
                }

                DetailsButton {
                    Task {
                        await info.record.openInTerminal()
                    }
                } label: {
                    Label("Terminal", systemImage: "terminal")
                }

                DetailsButton {
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
