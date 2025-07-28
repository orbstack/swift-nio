//
//  NewMainViewController.swift
//  MacVirt
//
//  Created by Andrew Zheng on 11/23/23.
//

import AppKit
import Combine
import Defaults
import SwiftUI

enum Panel {
    case sidebar
    case inspector
}

class NewMainViewController: NSViewController {
    var model: VmViewModel
    var navModel: MainNavViewModel
    var actionTracker: ActionTracker

    var horizontalConstraint: NSLayoutConstraint!
    var verticalConstraint: NSLayoutConstraint!

    let splitViewController = SplitViewController()

    var cancellables = Set<AnyCancellable>()

    // MARK: - Toolbar

    // initial empty toolbar should have a different ID, so it doesn't affect first selected tab
    var toolbar = NSToolbar(identifier: "__default")

    var isFirstUpdate = true

    // polyfill for macOS <14, with Show/Hide title
    lazy var toggleInspectorButton = makeToolbarItem(
        itemIdentifier: .toggleInspectorButton,
        icon: "sidebar.right",
        title: "Toggle Inspector",
        action: #selector(actionToggleInspector),
        requiresVmRunning: false
    )

    lazy var containersPlusButton = makeToolbarItem(
        itemIdentifier: .dockerContainersNew,
        icon: "plus",
        title: "New",
        action: #selector(actionDockerContainersNew)
    )

    lazy var containersFolderButton = makeToolbarItem(
        itemIdentifier: .dockerContainersOpen,
        icon: "externaldrive",
        title: "Files",
        action: #selector(actionDockerContainersOpen)
    )

    lazy var containersOpenWindowButton = makeToolbarItem(
        itemIdentifier: .dockerContainersOpenWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionDockerContainersOpenWindow)
    )

    private lazy var containersSortDelegate = EnumMenuDelegate<DockerContainerSortDescriptor>(
        key: .dockerContainersSortDescriptor)
    lazy var containersSortMenu = {
        let menu = NSMenu()
        menu.autoenablesItems = false
        menu.delegate = containersSortDelegate
        return self.makeMenuToolbarItem(
            itemIdentifier: .dockerContainersSort, icon: "arrow.up.arrow.down", title: "Sort",
            requiresVmRunning: false, menu: menu)
    }()

    lazy var containersFilterMenu = {
        let menuItem1 = NSMenuItem(
            title: "Show Stopped Containers", action: #selector(actionDockerContainersFilter1),
            keyEquivalent: "")
        menuItem1.target = self

        let item = makeMenuToolbarItem(
            itemIdentifier: .dockerContainersFilter,
            icon: "line.3.horizontal.circle",
            title: "Filter",
            menuItems: [menuItem1]
        )

        Defaults.publisher(.dockerFilterShowStopped).sink { [weak menuItem1] change in
            menuItem1?.state = change.newValue ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var containersTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .dockerContainersTabs,
            titles: ContainerTabId.allCases.map { $0.description }, selectionMode: .selectOne,
            labels: ContainerTabId.allCases.map { $0.description }, target: self,
            action: #selector(actionDockerContainersTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var volumesFolderButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesOpen,
        icon: "externaldrive",
        title: "Files",
        action: #selector(actionDockerVolumesOpen)
    )

    lazy var volumesPlusButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesNew,
        icon: "plus",
        title: "New",
        action: #selector(actionDockerVolumesNew)
    )

    lazy var volumesImportButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesImport,
        icon: "square.and.arrow.down",
        title: "Import",
        action: #selector(actionDockerVolumesImport)
    )

    lazy var volumesTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .dockerVolumesTabs, titles: VolumeTabId.allCases.map { $0.description },
            selectionMode: .selectOne, labels: VolumeTabId.allCases.map { $0.description },
            target: self, action: #selector(actionDockerVolumesTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var volumesOpenWindowButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesOpenWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionDockerVolumesOpenWindow)
    )

    private lazy var volumesSortDelegate = EnumMenuDelegate<DockerGenericSortDescriptor>(
        key: .dockerVolumesSortDescriptor)
    lazy var volumesSortMenu = {
        let menu = NSMenu()
        menu.delegate = volumesSortDelegate
        return self.makeMenuToolbarItem(
            itemIdentifier: .dockerVolumesSort, icon: "arrow.up.arrow.down", title: "Sort",
            requiresVmRunning: false, menu: menu)
    }()

    lazy var imagesFolderButton = makeToolbarItem(
        itemIdentifier: .dockerImagesOpen,
        icon: "externaldrive",
        title: "Files",
        action: #selector(actionDockerImagesOpen)
    )

    lazy var imagesImportButton = makeToolbarItem(
        itemIdentifier: .dockerImagesImport,
        icon: "square.and.arrow.down",
        title: "Import",
        action: #selector(actionDockerImagesImport)
    )

    private lazy var imagesSortDelegate = EnumMenuDelegate<DockerGenericSortDescriptor>(
        key: .dockerImagesSortDescriptor)
    lazy var imagesSortMenu = {
        let menu = NSMenu()
        menu.delegate = imagesSortDelegate
        return self.makeMenuToolbarItem(
            itemIdentifier: .dockerImagesSort, icon: "arrow.up.arrow.down", title: "Sort",
            requiresVmRunning: false, menu: menu)
    }()

    lazy var imagesTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .dockerImagesTabs, titles: ImageTabId.allCases.map { $0.description },
            selectionMode: .selectOne, labels: ImageTabId.allCases.map { $0.description },
            target: self, action: #selector(actionDockerImagesTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var imagesOpenWindowButton = makeToolbarItem(
        itemIdentifier: .dockerImagesOpenWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionDockerImagesOpenWindow)
    )

    lazy var networksPlusButton = makeToolbarItem(
        itemIdentifier: .dockerNetworksNew,
        icon: "plus",
        title: "New",
        action: #selector(actionDockerNetworksNew)
    )

    private lazy var networksSortDelegate = EnumMenuDelegate<DockerNetworkSortDescriptor>(
        key: .dockerNetworksSortDescriptor)
    lazy var networksSortMenu = {
        let menu = NSMenu()
        menu.delegate = networksSortDelegate
        return self.makeMenuToolbarItem(
            itemIdentifier: .dockerNetworksSort, icon: "arrow.up.arrow.down", title: "Sort",
            requiresVmRunning: false, menu: menu)
    }()

    lazy var networksTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .dockerNetworksTabs,
            titles: NetworkTabId.allCases.map { $0.description }, selectionMode: .selectOne,
            labels: NetworkTabId.allCases.map { $0.description }, target: self,
            action: #selector(actionDockerNetworksTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var podsStartToggle = {
        let item = NSToolbarItem(itemIdentifier: .k8sEnable)
        let toggle = NSSwitch()
        toggle.target = self
        toggle.action = #selector(actionK8sToggle)
        item.view = toggle
        item.label = "Enable Kubernetes"
        item.toolTip = "Enable Kubernetes"

        model.$config.sink { [weak toggle] config in
            toggle?.isEnabled = config != nil
            toggle?.state = (config?.k8sEnable ?? true) ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var podsFilterMenu = {
        let menuItem1 = NSMenuItem(
            title: "Show System Namespace", action: #selector(actionK8sPodsFilter1),
            keyEquivalent: "")
        menuItem1.target = self

        let item = makeMenuToolbarItem(
            itemIdentifier: .k8sPodsFilter,
            icon: "line.3.horizontal.circle",
            title: "Filter",
            menuItems: [menuItem1]
        )

        Defaults.publisher(.k8sFilterShowSystemNs).sink { [weak menuItem1] change in
            menuItem1?.state = change.newValue ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var podsTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .k8sPodsTabs, titles: PodsTabId.allCases.map { $0.description },
            selectionMode: .selectOne, labels: PodsTabId.allCases.map { $0.description },
            target: self, action: #selector(actionK8sPodsTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var k8sPodsOpenWindowButton = makeToolbarItem(
        itemIdentifier: .k8sPodsOpenWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionK8sPodsOpenWindow)
    )

    lazy var servicesFilterMenu = {
        let menuItem1 = NSMenuItem(
            title: "Show System Namespace", action: #selector(actionK8sServicesFilter1),
            keyEquivalent: "")
        menuItem1.target = self

        menuItem1.state = .on

        let item = makeMenuToolbarItem(
            itemIdentifier: .k8sServicesFilter,
            icon: "line.3.horizontal.circle",
            title: "Filter",
            menuItems: [menuItem1]
        )

        Defaults.publisher(.k8sFilterShowSystemNs).sink { [weak menuItem1] change in
            menuItem1?.state = change.newValue ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var servicesTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .k8sServicesTabs, titles: ServicesTabId.allCases.map { $0.description },
            selectionMode: .selectOne, labels: ServicesTabId.allCases.map { $0.description },
            target: self, action: #selector(actionK8sServicesTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var k8sServicesOpenWindowButton = makeToolbarItem(
        itemIdentifier: .k8sServicesOpenWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionK8sServicesOpenWindow)
    )

    lazy var machinesFolderButton = makeToolbarItem(
        itemIdentifier: .machinesOpen,
        icon: "externaldrive",
        title: "Files",
        action: #selector(actionMachinesOpen)
    )

    lazy var machinesImportButton = makeToolbarItem(
        itemIdentifier: .machinesImport,
        icon: "square.and.arrow.down",
        title: "Import",
        action: #selector(actionMachinesImport)
    )

    lazy var machinesPlusButton = makeToolbarItem(
        itemIdentifier: .machinesNew,
        icon: "plus",
        title: "New",
        action: #selector(actionMachinesNew)
    )

    lazy var machinesTabs = {
        let group = NSToolbarItemGroup(
            itemIdentifier: .machinesTabs, titles: MachineTabId.allCases.map { $0.description },
            selectionMode: .selectOne, labels: MachineTabId.allCases.map { $0.description },
            target: self, action: #selector(actionMachinesTabs))
        group.setSelected(true, at: 0)
        return group
    }()

    lazy var machinesOpenInNewWindowButton = makeToolbarItem(
        itemIdentifier: .machinesOpenInNewWindow,
        icon: "arrow.up.forward.app",
        title: "Open in New Window",
        action: #selector(actionMachinesOpenInNewWindow)
    )

    lazy var commandsHelpButton = makeToolbarItem(
        itemIdentifier: .cliHelp,
        icon: "questionmark.circle",
        title: "Docs",
        action: #selector(actionCliHelp)
    )

    lazy var activityMonitorStopButton = {
        let item = makeToolbarItem(
            itemIdentifier: .activityMonitorStop,
            icon: "xmark.octagon",
            title: "Stop",
            action: #selector(actionActivityMonitorStop),
            // will break isEnabled toggle below
            requiresVmRunning: false
        )

        model.$activityMonitorStopEnabled.sink { [weak item] enabled in
            item?.isEnabled = enabled
        }.store(in: &cancellables)

        return item
    }()

    lazy var searchItem = {
        let item = NSSearchToolbarItem(itemIdentifier: .searchItem)
        item.searchField.delegate = self
        return item
    }()

    lazy var licenseBadgeItem = {
        let item = NSToolbarItem(itemIdentifier: .licenseBadge)
        item.view = NSHostingView(rootView: LicenseBadgeView(vmModel: model))
        return item
    }()

    // MARK: - Init

    init(model: VmViewModel, navModel: MainNavViewModel, actionTracker: ActionTracker) {
        self.model = model
        self.navModel = navModel
        self.actionTracker = actionTracker

        super.init(nibName: nil, bundle: nil)

        navModel.expandInspector.sink { [weak self] in
            self?.splitViewController.itemC.animator().isCollapsed = false
        }.store(in: &cancellables)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let view = WindowGrabberView()
        self.view = view

        addChildViewController(splitViewController, in: view)

        view.onMoveToWindow = { [weak self] window in
            guard let self else { return }
            self.movedToWindow(window: window)
        }

        listen()
    }

    func movedToWindow(window: NSWindow) {
        window.title = "OrbStack"

        // enable full height sidebar
        window.styleMask.insert(.fullSizeContentView)

        // set it here also because when `updateToolbarFromSelectionChange` is first called,
        // the window is still nil.
        window.toolbar = toolbar
    }

    override var representedObject: Any? {
        didSet {
            // Update the view, if already loaded.
        }
    }
}

extension NewMainViewController {
    func makeToolbarItem(
        itemIdentifier: NSToolbarItem.Identifier,
        icon: String,
        title: String,
        action: Selector?,
        requiresVmRunning: Bool = true
    ) -> NSToolbarItem {
        let item = NSToolbarItem(itemIdentifier: itemIdentifier)
        let image = NSImage(systemSymbolName: icon, accessibilityDescription: nil)!
        item.image = image
        item.isBordered = true
        item.target = self
        item.action = action
        item.label = title  // won't be shown actually because toolbar is `.iconOnly`
        item.toolTip = title

        if requiresVmRunning {
            model.$state.sink { [weak item] state in
                item?.isEnabled = state == .running
            }.store(in: &cancellables)
        }

        return item
    }

    func makeMenuToolbarItem(
        itemIdentifier: NSToolbarItem.Identifier,
        icon: String,
        title: String,
        requiresVmRunning: Bool = true,
        menu: NSMenu
    ) -> NSMenuToolbarItem {
        let item = NSMenuToolbarItem(itemIdentifier: itemIdentifier)
        item.menu = menu
        item.image = NSImage(systemSymbolName: icon, accessibilityDescription: nil)!
        item.isBordered = true
        item.toolTip = title
        item.label = title

        if requiresVmRunning {
            model.$state.sink { [weak item] state in
                item?.isEnabled = state == .running
            }.store(in: &cancellables)
        }

        return item
    }

    func makeMenuToolbarItem(
        itemIdentifier: NSToolbarItem.Identifier,
        icon: String,
        title: String,
        requiresVmRunning: Bool = true,
        menuItems: [NSMenuItem]
    ) -> NSMenuToolbarItem {
        let menu = NSMenu(title: title)
        menu.items = menuItems

        return self.makeMenuToolbarItem(
            itemIdentifier: itemIdentifier, icon: icon, title: title,
            requiresVmRunning: requiresVmRunning, menu: menu)
    }
}

private class EnumMenuDelegate<
    T: CustomStringConvertible & Defaults.Serializable & Equatable & CaseIterable
>: NSObject, NSMenuDelegate {
    let key: Defaults.Key<T>

    init(key: Defaults.Key<T>) {
        self.key = key
    }

    func menuWillOpen(_ menu: NSMenu) {
        let currentValue = Defaults[key]
        menu.items = T.allCases.map { method in
            let item = ClosureMenuItem(title: method.description) { [key] in
                Defaults[key] = method
            }
            item.state = (currentValue == method) ? .on : .off
            return item
        }
        for item in menu.items {
            item.isHidden = false
        }
    }
}
