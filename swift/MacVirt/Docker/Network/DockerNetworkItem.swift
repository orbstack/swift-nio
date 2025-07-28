//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerNetworkItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    @StateObject private var windowHolder = WindowHolder()

    var network: DKNetwork
    var selection: Set<String> {
        listModel.selection as! Set<String>
    }

    static func == (lhs: DockerNetworkItem, rhs: DockerNetworkItem) -> Bool {
        lhs.network.id == rhs.network.id
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(network: network) != nil
        let isInUse = network.containers?.isEmpty ?? false

        HStack {
            HStack {
                VStack(alignment: .leading) {
                    Text(network.name)
                        .font(.body)
                        .truncationMode(.tail)
                        .lineLimit(1)

                    if let ipamConfig = network.ipam?.config?.first {
                        Text(ipamConfig.subnet)
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                    }
                }
            }

            Spacer()

            ProgressIconButton(
                systemImage: "trash.fill",
                actionInProgress: actionInProgress,
                role: .destructive
            ) {
                finishDelete()
            }
            .disabled(actionInProgress || isInUse)
            .help(isInUse ? "Network in use" : "Delete network")
        }
        .padding(.vertical, 8)
        .akListContextMenu {
            Button {
                finishDelete()
            } label: {
                Label("Delete", systemImage: "trash")
            }.disabled(actionInProgress || isInUse)

            Divider()

            Button {
                NSPasteboard.copy(network.name)
            } label: {
                Label("Copy Name", systemImage: "doc.on.doc")
            }

            Button {
                NSPasteboard.copy(network.id)
            } label: {
                Label("Copy ID", systemImage: "doc.on.doc")
            }

            if let ipamConfig = network.ipam?.config?.first {
                Button {
                    NSPasteboard.copy(ipamConfig.subnet)
                } label: {
                    Label("Copy Subnet", systemImage: "doc.on.doc")
                }
            } else {
                Button {
                    // no-op
                } label: {
                    Label("Copy Subnet", systemImage: "doc.on.doc")
                }
                .disabled(true)
            }
        }
        .windowHolder(windowHolder)
    }

    private func finishDelete() {
        for id in resolveActionList() {
            NSLog("remove network \(id)")
            Task { @MainActor in
                await actionTracker.with(networkId: id, action: .delete) {
                    await vmModel.tryDockerNetworkRemove(id)
                }
            }
        }
    }

    private func isSelected() -> Bool {
        selection.contains(network.id)
    }

    private func resolveActionList() -> Set<String> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            return selection
        } else {
            return [network.id]
        }
    }
}
