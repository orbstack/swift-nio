//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerNetworkDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var actionTracker: ActionTracker

    @StateObject private var windowHolder = WindowHolder()

    let network: DKNetwork

    var body: some View {
        DetailsStack {
            DetailsKvSection {
                DetailsRow("Name", copyableText: network.name)

                DetailsRow("ID") {
                    CopyableText(
                        String(network.id.trimmingPrefix("sha256:").prefix(12)), copyAs: network.id
                    )
                    .font(.body.monospaced())
                }

                DetailsRow("Created", text: "\(network.formattedCreated) (\(network.formattedCreatedLong))")

                if let ipamConfig = network.ipam?.config?.first {
                    DetailsRow("Subnet", copyableText: ipamConfig.subnet)
                    DetailsRow("Gateway", copyableText: ipamConfig.gateway)
                }
            }

            DetailsKvSection {
                DetailsRow("Driver", text: network.driver)
                DetailsRow("Scope", text: network.scope ?? "")
            }

            if let containers = network.containers,
                !containers.isEmpty
            {
                DetailsListSection("Used By") {
                    ForEach(Array(containers.values), id: \.endpointId) { container in
                        Text(container.name)
                    }
                }
            }

            if !network.labels.isEmpty {
                DetailsLabelsSection(labels: network.labels)
            }

            if !network.options.isEmpty {
                let sortedOptions = network.options.sorted { $0.key < $1.key }.map {
                    KeyValueItem(key: $0.key, value: $0.value)
                }
                DetailsKvTableSection("Options", items: sortedOptions)
            }
        }
        .windowHolder(windowHolder)
    }
}
