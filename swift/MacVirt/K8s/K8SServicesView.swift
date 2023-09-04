//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

private struct GettingStartedHintBox: View {
    var body: some View {
        VStack(spacing: 8) {
            Text("Get started with an example")
            .font(.title2)
            .bold()
            Text("kubectl expose pod nginx --type=NodePort --port=80")
            .textSelectionWithWorkaround()
            .font(.body.monospaced())
        }
        .padding(.vertical, 24)
        .padding(.horizontal, 48)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct K8SServicesList: View {
    let filterIsSearch: Bool
    let listItems: [SectionGroup<K8SService>]
    @Binding var selection: Set<K8SResourceId>

    var body: some View {
        VStack(spacing: 0) {
            if !listItems.isEmpty {
                List(selection: $selection) {
                    ForEach(listItems) { group in
                        Section(header: Text(group.title)) {
                            ForEach(group.items) { item in
                                // single list row content item for perf: https://developer.apple.com/videos/play/wwdc2023/10160/
                                K8SServiceItemView(service: item,
                                        selection: selection)
                                .equatable()
                            }
                        }
                    }
                }
            } else {
                Spacer()

                HStack {
                    Spacer()
                    if filterIsSearch {
                        ContentUnavailableViewCompat.search
                    } else {
                        ContentUnavailableViewCompat("No Services", systemImage: "network")
                    }
                    Spacer()
                }

                Spacer()

                // don't show getting started hint if empty is caused by filter
                if !filterIsSearch {
                    HStack {
                        Spacer()
                        GettingStartedHintBox()
                        Spacer()
                    }
                    .padding(.bottom, 48)
                }
            }
        }
    }
}

struct K8SServicesView: View {
    @Default(.k8sFilterShowSystemNs) private var showSystemNs

    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: Set<K8SResourceId> = []
    @State private var searchQuery = ""

    var body: some View {
        K8SStateWrapperView(\.k8sServices) { services, _ in
            let filteredServices = services.filter { service in
                searchQuery.isEmpty ||
                        service.metadata.name.localizedCaseInsensitiveContains(searchQuery) ||
                        service.metadata.namespace.localizedCaseInsensitiveContains(searchQuery)
            }

            let listItems = K8SResourceLists.groupItems(filteredServices,
                    showSystemNs: showSystemNs)

            // 0 spacing to fix bg color gap between list and getting started hint
            K8SServicesList(filterIsSearch: !searchQuery.isEmpty,
                    listItems: listItems,
                    selection: $selection)
        }
        .navigationTitle("Services")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }
}
