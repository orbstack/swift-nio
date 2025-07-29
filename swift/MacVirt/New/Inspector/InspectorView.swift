//
//  InspectorView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import Defaults
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

private struct InspectorSelectionList<Item, ID: Hashable, Seq: Sequence<Item>, Content: View>: View
{
    private let items: Seq?
    private let selection: Set<AnyHashable>
    private let key: KeyPath<Item, ID>
    @ViewBuilder private let content: (Item) -> Content

    init(
        _ items: Seq?,
        key: KeyPath<Item, ID>,
        selection: Set<AnyHashable>,
        @ViewBuilder content: @escaping (Item) -> Content
    ) {
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
            content(selectedItems[0])
        } else {
            // multiple selections
            // TODO: outline view
            ContentUnavailableViewCompat("\(selectedItems.count) Selected")
        }
    }
}

extension InspectorSelectionList where Item: Identifiable, ID == Item.ID {
    fileprivate init(
        _ items: Seq?,
        selection: Set<AnyHashable>,
        @ViewBuilder content: @escaping (Item) -> Content
    ) {
        self.init(items, key: \.id, selection: selection, content: content)
    }
}

struct InspectorView: View {
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel

    @Default(.selectedTab) private var selectedTab

    var body: some View {
        Group {
            switch selectedTab {
            case .dockerContainers:
                let selection = navModel.inspectorSelection

                if selection.isEmpty {
                    // empty, or selections not in data source
                    ContentUnavailableViewCompat("No Selection")
                } else if selection.count == 1,
                    let selItem = selection.first as? DockerContainerId
                {
                    // single selection
                    switch selItem {
                    case let .compose(project):
                        switch model.containerTab {
                        case .info:
                            DockerComposeGroupDetails(project: project)
                        case .logs:
                            DockerComposeLogsTab(project: project)
                        default:
                            ContentUnavailableViewCompat("Select a Container")
                        }

                    case .k8sGroup:
                        K8SGroupDetails()

                    case let .container(id):
                        if let container = model.dockerContainers?.byId[id] {
                            switch model.containerTab {
                            case .info:
                                DockerContainerDetails(container: container)
                            case .logs:
                                DockerContainerLogsTab(container: container)
                            case .terminal:
                                DockerContainerTerminalTab(container: container)
                            case .files:
                                DockerContainerFilesTab(container: container)
                            }
                        } else {
                            ContentUnavailableViewCompat("No Selection")
                        }

                    default:
                        EmptyView()
                    }
                } else {
                    // multiple selections
                    // TODO: outline view
                    ContentUnavailableViewCompat("\(selection.count) Selected")
                }
            case .dockerVolumes:
                InspectorSelectionList(
                    model.dockerVolumes?.values, selection: navModel.inspectorSelection
                ) {
                    switch model.volumesTab {
                    case .info:
                        DockerVolumeDetails(volume: $0)
                    case .files:
                        DockerVolumeFilesTab(volume: $0)
                    }
                }
            case .dockerImages:
                InspectorSelectionList(
                    model.dockerImages?.values, selection: navModel.inspectorSelection
                ) {
                    switch model.imagesTab {
                    case .info:
                        DockerImageDetails(image: $0)
                    case .terminal:
                        DockerImageTerminalTab(image: $0)
                    case .files:
                        DockerImageFilesTab(image: $0)
                    }
                }
            case .dockerNetworks:
                InspectorSelectionList(
                    model.dockerNetworks?.values, selection: navModel.inspectorSelection
                ) {
                    switch model.networksTab {
                    case .info:
                        DockerNetworkDetails(network: $0)
                    }
                }
            case .k8sPods:
                InspectorSelectionList(model.k8sPods, selection: navModel.inspectorSelection) {
                    switch model.podsTab {
                    case .info:
                        K8SPodDetails(pod: $0)
                    }
                }
            case .k8sServices:
                InspectorSelectionList(model.k8sServices, selection: navModel.inspectorSelection) {
                    switch model.servicesTab {
                    case .info:
                        K8SServiceDetails(service: $0)
                    }
                }
            case .machines:
                InspectorSelectionList(
                    model.machines?.values, selection: navModel.inspectorSelection
                ) {
                    switch model.machineTab {
                    case .info:
                        MachineDetails(info: $0)
                    case .logs:
                        MachineLogsTab(machine: $0)
                    case .terminal:
                        MachineTerminalTab(machine: $0)
                    case .files:
                        MachineFilesTab(machine: $0)
                    }
                }

            // SwiftUI inspectorView affordance
            default:
                if let view = navModel.inspectorView {
                    view.value
                } else {
                    EmptyView()
                }
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

struct UniqueEquatable<T>: Equatable {
    let id = UUID()
    let value: T

    static func == (lhs: UniqueEquatable<T>, rhs: UniqueEquatable<T>) -> Bool {
        lhs.id == rhs.id
    }
}

struct InspectorViewKey: PreferenceKey {
    static var defaultValue: UniqueEquatable<AnyView>?

    static func reduce(
        value: inout UniqueEquatable<AnyView>?, nextValue: () -> UniqueEquatable<AnyView>?
    ) {
        let nextVal = nextValue()
        if let nextVal {
            value = nextVal
        }
    }
}

extension View {
    func inspectorSelection<ID: Hashable>(_ selection: Set<ID>) -> some View {
        preference(key: InspectorSelectionKey.self, value: selection as Set<AnyHashable>)
    }

    func inspectorView<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        preference(
            key: InspectorViewKey.self,
            value: UniqueEquatable(value: AnyView(content()))
        )
    }
}

struct AKNavigationTitleKey: PreferenceKey {
    static var defaultValue: String? = nil

    static func reduce(value: inout String?, nextValue: () -> String?) {
        let nextVal = nextValue()
        if let nextVal {
            value = nextVal
        }
    }
}

extension View {
    func akNavigationTitle(_ title: String?) -> some View {
        preference(key: AKNavigationTitleKey.self, value: title)
    }
}
