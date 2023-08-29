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
            Text("kubectl run nginx --image=nginx")
            .font(.body.monospaced())
            .textSelection(.enabled)
        }
        .padding(.vertical, 24)
        .padding(.horizontal, 48)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct K8SPodsList: View {
    let filterIsSearch: Bool
    let runningCount: Int
    let listItems: [SectionGroup<K8SPod>]
    let selection: Binding<Set<K8SResourceId>>

    var body: some View {
        VStack(spacing: 0) {
            if !listItems.isEmpty {
                List(selection: selection) {
                    ForEach(listItems, id: \.title) { group in
                        Section(header: Text(group.title)) {
                            ForEach(group.items) { item in
                                // single list row content item for perf: https://developer.apple.com/videos/play/wwdc2023/10160/
                                K8SPodItemView(pod: item, selection: selection.wrappedValue)
                                .equatable()
                            }
                        }
                    }
                }
                .navigationSubtitle(runningCount == 0 ? "None running" : "\(runningCount) running")
            } else {
                Spacer()

                HStack {
                    Spacer()
                    if filterIsSearch {
                        ContentUnavailableViewCompat.search
                    } else {
                        ContentUnavailableViewCompat("No Pods", systemImage: "helm")
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

struct K8SPodsView: View {
    @Default(.k8sFilterShowSystemNs) private var showSystemNs

    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: Set<K8SResourceId> = []
    @State private var searchQuery = ""

    var body: some View {
        K8SStateWrapperView(\.k8sPods) { pods, _ in
            let runningCount = pods.filter { $0.uiState == .running }.count

            let filteredPods = pods.filter { pod in
                searchQuery.isEmpty ||
                        pod.metadata.name.localizedCaseInsensitiveContains(searchQuery) ||
                        pod.metadata.namespace.localizedCaseInsensitiveContains(searchQuery)
            }

            let listItems = K8SResourceLists.groupItems(filteredPods,
                    showSystemNs: showSystemNs)

            // 0 spacing to fix bg color gap between list and getting started hint
            K8SPodsList(filterIsSearch: !searchQuery.isEmpty,
                    runningCount: runningCount,
                    listItems: listItems,
                    selection: $selection)
        } onRefresh: {
            await vmModel.tryRefreshList()
        }
        .navigationTitle("Pods")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }
}
