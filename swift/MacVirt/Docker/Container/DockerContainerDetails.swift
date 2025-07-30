//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerContainerDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let container: DKContainer

    @State private var portSelection: Set<String> = []
    @State private var mountSelection: Set<String> = []

    var body: some View {
        DetailsStack {
            DetailsKvSection {
                if let name = container.names.first {
                    DetailsRow("Name", copyableText: String(name.trimmingPrefix("/")))
                }

                DetailsRow("ID") {
                    CopyableText(String(container.id.prefix(12)), copyAs: container.id)
                        .font(.body.monospaced())
                }

                DetailsRow("Image", copyableText: container.image)
                DetailsRow("Status", text: container.status)
            }

            // needs to be running w/ ip to have domain
            if let ipAddress = container.ipAddress {
                DetailsKvSection {
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
                Section {
                    Table(container.ports, selection: $portSelection) {
                        TableColumn("Host Port") { port in
                            CustomLink("\(port.localPortFormatted)") {
                                port.openUrl()
                            }
                        }

                        TableColumn("Container Port") { port in
                            // avoid number formatting
                            Text(String(port.privatePort))
                        }

                        TableColumn("Protocol") { port in
                            Text(port.type.uppercased())
                        }
                    }
                } header: {
                    Text("Port Forwards")
                }
                .onCopyCommand {
                    let selectedItems = portSelection.compactMap { id in container.ports.first { $0.id == id } }
                    return [
                        NSItemProvider(
                            object: selectedItems.map { $0.formattedAsCli }.joined(separator: "\n")
                                as NSString)
                    ]
                }
            }

            if !container.mounts.isEmpty {
                Section {
                    Table(container.mounts, selection: $mountSelection) {
                        TableColumn("Source") { mount in
                            CopyableText(copyAs: mount.getOpenPath()) {
                                CustomLink(mount.source) {
                                    mount.openSourceDirectory()
                                }
                            }
                        }

                        TableColumn("Destination") { mount in
                            Text(mount.destination)
                        }
                    }
                    .onCopyCommand {
                        let selectedItems = mountSelection.compactMap { id in container.mounts.first { $0.id == id } }
                        return [
                            NSItemProvider(
                                object: selectedItems.map { "\($0.source):\($0.destination)" }.joined(separator: "\n")
                                    as NSString)
                        ]
                    }
                } header: {
                    Text("Mounts")
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
