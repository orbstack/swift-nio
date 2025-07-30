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
            DetailsKvSection {
                DetailsRow("ID") {
                    CopyableText(String(container.id.prefix(12)), copyAs: container.id)
                        .font(.body.monospaced())
                }

                DetailsRow("Status", text: container.status)
                DetailsRow("Image", copyableText: container.image)

                // needs to be running w/ ip to have domain
                if let ipAddress = container.ipAddress {
                    if vmModel.netBridgeAvailable,
                        let domain = container.preferredDomain,
                        let url = URL(string: "\(container.getPreferredProto(vmModel))://\(domain)")
                    {
                        DetailsRow("Domain") {
                            CopyableText(copyAs: domain) {
                                CustomLink(domain, url: url)
                            }
                        }
                    }

                    DetailsRow("IP", copyableText: ipAddress)
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

            if let labels = container.labels,
                !labels.isEmpty
            {
                DetailsLabelsSection(labels: labels)
            }
        }
    }
}
