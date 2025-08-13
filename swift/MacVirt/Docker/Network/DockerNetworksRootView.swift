//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerNetworksRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var actionTracker: ActionTracker

    @Default(.dockerNetworksSortDescriptor) private var sortDescriptor

    @State private var selection: Set<String> = []

    var body: some View {
        let searchQuery = vmModel.searchText

        DockerStateWrapperView(\.dockerNetworks) { networks, _ in
            let filteredNetworks = self.filterNetworks(networks.values, searchQuery: searchQuery)

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                if !filteredNetworks.isEmpty {
                    let listData = filteredNetworks.grouped { $0.driver }.map { driver, networks in
                        AKSection(driver, networks)
                    }.sorted { $0.title! < $1.title! }

                    // 46 is empirically correct, matches auto height. not sure where it comes from
                    AKList(listData, selection: $selection, rowHeight: 46) { network in
                        DockerNetworkItem(network: network)
                            .equatable()
                            .environmentObject(vmModel)
                            .environmentObject(windowTracker)
                            .environmentObject(actionTracker)
                    }
                    .navigationSubtitle("\(filteredNetworks.count) total")
                    .inspectorSelection(selection)
                } else {
                    Spacer()

                    HStack {
                        Spacer()
                        if searchQuery.isEmpty {
                            ContentUnavailableViewCompat(
                                "No Networks",
                                systemImage: "point.3.filled.connected.trianglepath.dotted")
                        } else {
                            ContentUnavailableViewCompat.search
                        }
                        Spacer()
                    }.padding(16)

                    Spacer()
                }
            }
        }
        .navigationTitle("Networks")
        .sheet(isPresented: $vmModel.presentCreateNetwork) {
            CreateNetworkView(isPresented: $vmModel.presentCreateNetwork)
        }
    }

    private func filterNetworks(_ networks: any Sequence<DKNetwork>, searchQuery: String)
        -> [DKNetwork]
    {
        var networks = networks.filter { (network: DKNetwork) in
            searchQuery.isEmpty
                || network.name.localizedCaseInsensitiveContains(searchQuery)
                || (network.ipam?.config?.contains {
                    $0.subnet.localizedCaseInsensitiveContains(searchQuery)
                        || $0.gateway.localizedCaseInsensitiveContains(searchQuery)
                } ?? false)
        }
        networks.sort(accordingTo: sortDescriptor)
        return networks
    }
}

extension [DKNetwork] {
    fileprivate mutating func sort(accordingTo descriptor: DockerNetworkSortDescriptor) {
        switch descriptor {
        case .name:
            self.sort(by: { $0.name < $1.name })
        case .containers:
            self.sort(by: { $0.containers?.count ?? 0 > $1.containers?.count ?? 0 })
        case .dateDescending:
            self.sort(by: {
                $0.createdDate ?? Date.distantPast > $1.createdDate ?? Date.distantPast
            })
        case .dateAscending:
            self.sort(by: {
                $0.createdDate ?? Date.distantPast < $1.createdDate ?? Date.distantPast
            })
        }
    }
}
