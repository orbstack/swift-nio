//
//  InspectorView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import SwiftUI

class InspectorViewController: NSViewController {
    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let contentView = InspectorView()
        let hostingView = NSHostingView(rootView: contentView)
        view = hostingView
    }
}

private struct InspectorSelectionList<Item: Identifiable, ID: Hashable, Content: View>: View {
    private let items: [Item]?
    private let selection: Set<AnyHashable>
    private let key: KeyPath<Item, ID>
    @ViewBuilder private let content: (Item) -> Content

    init(_ items: [Item]?,
         key: KeyPath<Item, ID>,
         selection: Set<AnyHashable>,
         @ViewBuilder content: @escaping (Item) -> Content)
    {
        self.items = items
        self.key = key
        self.selection = selection
        self.content = content
    }

    var body: some View {
        let selectedItems = items?.filter { selection.contains($0[keyPath: key]) } ?? []

        if selectedItems.isEmpty {
            // empty, or selections not in data source
            ContentUnavailableViewCompat("No Selection")
        } else if selectedItems.count == 1 {
            // single selection
            ScrollView {
                content(selectedItems[0])
            }
        } else {
            // multiple selections
            // TODO: outline view
            ContentUnavailableViewCompat("\(selectedItems.count) Selected")
        }
    }
}

struct InspectorView: View {
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel

    var body: some View {
        VStack {
            switch model.selectedTab {
            case .dockerContainers:
                let selection = navModel.inspectorSelection

                if selection.isEmpty {
                    // empty, or selections not in data source
                    ContentUnavailableViewCompat("No Selection")
                } else if selection.count == 1,
                          let selItem = selection.first as? DockerContainerId
                {
                    // single selection
                    ScrollView {
                        if case let .compose(project) = selItem {
                            DockerComposeGroupDetails(project: project)
                        } else {
                            let container = model.dockerContainers?.first { $0.cid == selItem }
                            if let container {
                                DockerContainerDetails(container: container)
                            } else {
                                ContentUnavailableViewCompat("No Container")
                            }
                        }
                    }
                } else {
                    // multiple selections
                    // TODO: outline view
                    ContentUnavailableViewCompat("\(selection.count) Selected")
                }
            case .dockerVolumes:
                InspectorSelectionList(model.dockerVolumes, key: \.id, selection: navModel.inspectorSelection) {
                    DockerVolumeDetails(volume: $0)
                }
            case .dockerImages:
                InspectorSelectionList(model.dockerImages, key: \.id, selection: navModel.inspectorSelection) {
                    DockerImageDetails(image: $0)
                }
            case .k8sPods:
                InspectorSelectionList(model.k8sPods, key: \.id, selection: navModel.inspectorSelection) {
                    K8SPodDetails(pod: $0)
                }
            case .k8sServices:
                InspectorSelectionList(model.k8sServices, key: \.id, selection: navModel.inspectorSelection) {
                    K8SServiceDetails(service: $0)
                }
            case .machines:
                InspectorSelectionList(model.containers, key: \.id, selection: navModel.inspectorSelection) {
                    MachineDetails(record: $0)
                }
            default:
                EmptyView()
            }
        }
        // you don't need this when you have a scroll view,
        // but make sure to expand to fill all space.
        // otherwise, the split view layout will break.
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct InspectorSelectionKey: PreferenceKey {
    static var defaultValue = Set<AnyHashable>()

    static func reduce(value: inout Set<AnyHashable>, nextValue: () -> Set<AnyHashable>) {
        let nextVal = nextValue()
        if !nextVal.isEmpty {
            value = nextVal
        }
    }
}

extension View {
    func inspectorSelection<ID: Hashable>(_ selection: Set<ID>) -> some View {
        preference(key: InspectorSelectionKey.self, value: selection as Set<AnyHashable>)
    }
}
