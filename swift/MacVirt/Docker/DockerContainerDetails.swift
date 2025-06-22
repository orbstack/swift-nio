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

            DetailsKvSection {
                DetailsRow("ID") {
                    CopyableText(String(container.id.prefix(12)), copyAs: container.id)
                        .font(.body.monospaced())
                }

                DetailsRow("Status", text: container.status)
                DetailsRow("Image", copyableText: container.image)

                // needs to be running w/ ip to have domain
                if let ipAddress = container.ipAddress,
                    let domain = container.preferredDomain,
                    let url = URL(string: "\(container.getPreferredProto(vmModel))://\(domain)")
                {
                    if vmModel.netBridgeAvailable {
                        DetailsRow("Domain") {
                            CopyableText(copyAs: domain) {
                                CustomLink(domain, url: url)
                            }
                        }
                    } else {
                        DetailsRow("IP", copyableText: ipAddress)
                    }
                }
            }

            DetailsButtonSection {
                DetailsButton {
                    container.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                DetailsButton {
                    if vmModel.isLicensed {
                        container.openDebugShell()
                    } else {
                        vmModel.presentRequiresLicense = true
                    }
                } label: {
                    Label("Debug", systemImage: "ladybug")
                }

                if isRunning {
                    DetailsButton {
                        container.openDebugShellFallback()
                    } label: {
                        Label("Terminal", systemImage: "terminal")
                    }

                    DetailsButton {
                        container.openFolder()
                    } label: {
                        Label("Files", systemImage: "folder")
                    }

                    if container.image == "docker/getting-started" {
                        // special case for more seamless onboarding
                        DetailsButton {
                            NSWorkspace.shared.open(URL(string: "http://localhost")!)
                        } label: {
                            Label("Tutorial", systemImage: "questionmark.circle")
                        }
                    }
                }
            }

            if !container.ports.isEmpty {
                DetailsListSection("Ports") {
                    ForEach(container.ports) { port in
                        CopyableText(copyAs: "\(port.localPort)") {
                            CustomLink(port.formatted) {
                                port.openUrl()
                            }
                        }
                    }
                }
            }

            if !container.mounts.isEmpty {
                DetailsListSection("Mounts") {
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
    }
}
