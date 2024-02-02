//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerContainerDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let container: DKContainer

    var body: some View {
        DetailsStack {
            let isRunning = container.running

            DetailsSection("Info") {
                SimpleKvTable(longestLabel: "Address") {
                    let domain = container.preferredDomain
                    let ipAddress = container.ipAddress

                    SimpleKvTableRow("Status") {
                        Text(container.status)
                    }

                    SimpleKvTableRow("ID") {
                        CopyableText(String(container.id.prefix(12)), copyAs: container.id)
                            .font(.body.monospaced())
                    }

                    SimpleKvTableRow("Image") {
                        CopyableText(container.image)
                            .frame(maxWidth: 300, alignment: .leading)
                            .truncationMode(.tail)
                    }

                    // needs to be running w/ ip to have domain
                    if let ipAddress,
                       let domain,
                       let url = URL(string: "\(container.getPreferredProto(vmModel))://\(domain)")
                    {
                        SimpleKvTableRow("Address") {
                            if vmModel.netBridgeAvailable {
                                CopyableText(copyAs: domain) {
                                    CustomLink(domain, url: url)
                                }
                            } else {
                                CopyableText(ipAddress)
                            }
                        }
                    }
                }
            }

            if !container.ports.isEmpty {
                DetailsSection("Ports") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(container.ports) { port in
                            CopyableText(copyAs: "\(port.localPort)") {
                                CustomLink(port.formatted) {
                                    port.openUrl()
                                }
                            }
                        }
                    }
                }
            }

            if !container.mounts.isEmpty {
                DetailsSection("Mounts") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(container.mounts) { mount in
                            CopyableText(copyAs: mount.getOpenPath()) {
                                CustomLink(mount.formatted) {
                                    mount.openSourceDirectory()
                                }
                            }
                        }
                    }
                }
            }

            DividedButtonStack {
                DividedRowButton {
                    container.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                if isRunning {
                    DividedRowButton {
                        container.openInTerminal()
                    } label: {
                        Label("Terminal", systemImage: "terminal")
                    }

                    DividedRowButton {
                        container.openFolder()
                    } label: {
                        Label("Files", systemImage: "folder")
                    }

                    if container.image == "docker/getting-started" {
                        // special case for more seamless onboarding
                        DividedRowButton {
                            NSWorkspace.shared.open(URL(string: "http://localhost")!)
                        } label: {
                            Label("Open Tutorial", systemImage: "questionmark.circle")
                        }
                    }
                }
            }
        }
    }
}
