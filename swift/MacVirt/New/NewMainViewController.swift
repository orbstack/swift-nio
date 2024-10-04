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

    lazy var containersFilterMenu = {
        let menuItem1 = NSMenuItem(
            title: "Show Stopped Containers", action: #selector(actionDockerContainersFilter1),
            keyEquivalent: "")
        menuItem1.target = self

        let item = makeMenuToolbarItem(
            itemIdentifier: .dockerContainersFilter,
            icon: "line.3.horizontal.circle",
            title: "Filter Containers",
            menuItems: [menuItem1]
        )

        Defaults.publisher(.dockerFilterShowStopped).sink { [weak menuItem1] change in
            menuItem1?.state = change.newValue ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var volumesFolderButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesOpen,
        icon: "folder",
        title: "Open Volumes",
        action: #selector(actionDockerVolumesOpen)
    )

    lazy var volumesPlusButton = makeToolbarItem(
        itemIdentifier: .dockerVolumesNew,
        icon: "plus",
        title: "New Volume",
        action: #selector(actionDockerVolumesNew)
    )

    lazy var imagesFolderButton = makeToolbarItem(
        itemIdentifier: .dockerImagesOpen,
        icon: "folder",
        title: "Open Images",
        action: #selector(actionDockerImagesOpen)
    )

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

    lazy var machinesPlusButton = makeToolbarItem(
        itemIdentifier: .machinesNew,
        icon: "plus",
        title: "New Machine",
        action: #selector(actionMachinesNew)
    )

    lazy var commandsHelpButton = makeToolbarItem(
        itemIdentifier: .cliHelp,
        icon: "questionmark.circle",
        title: "Go to Docs",
        action: #selector(actionCliHelp)
    )

    lazy var searchItem = {
        let item = NSSearchToolbarItem(itemIdentifier: .searchItem)
        item.searchField.delegate = self
        return item
    }()

    func makeIndividualSortingNSMenuItem(method sortMethod: DockerSortMethod, menu: NSMenu)
        -> NSMenuItem
    {
        let item = ClosureMenuItem(title: sortMethod.description) {
            self.model.dockerSortingMethod = sortMethod

            menu.items = self.makeAllSortingNSMenuItems(forMenu: menu)  // refresh item states

            // for some reason after clicking on an item it'll automatically hide just the first one????? so
            // this is why we need this workaround (wtf appkit??)
            for item in menu.items { item.isHidden = false }
        }
        // Don't set item.state here, it'll be set in menuWillOpen
        // this is so that, if a user selects "size" in another tab
        // and switches to one where it's not allowed (Docker Containers)
        // we'll automatically switch the state for the default one that the app switches to
        item.tag = sortMethod.rawValue
        return item
    }

    func makeAllSortingNSMenuItems(forMenu menu: NSMenu) -> [NSMenuItem] {
        let menuItems = DockerSortMethod.allCases.map { method in
            makeIndividualSortingNSMenuItem(method: method, menu: menu)
        }

        return menuItems
    }

    lazy var sortListItem = {
        let menu = NSMenu()
        menu.autoenablesItems = false
        menu.delegate = self
        menu.identifier = .sortListItemMenu
        menu.items = makeAllSortingNSMenuItems(forMenu: menu)

        return self.makeMenuToolbarItem(
            itemIdentifier: .sortList, icon: "arrow.up.arrow.down", title: "Sort",
            requiresVmRunning: false, menu: menu)
    }()

    lazy var licenseBadgeItem = {
        let item = NSToolbarItem(itemIdentifier: .licenseBadge)
        item.view = NSHostingView(rootView: LicenseBadgeView(vmModel: model))
        return item
    }()

    // MARK: - Init

    init(model: VmViewModel, navModel: MainNavViewModel) {
        self.model = model
        self.navModel = navModel
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

    let principalViewMinimumWidth = CGFloat(300)

    override func viewWillLayout() {
        super.viewWillLayout()

        let windowWidth = view.bounds.size.width
        let sidebarWidth = splitViewController.itemA.viewController.view.bounds.width
        let inspectorWidth = splitViewController.itemC.viewController.view.bounds.width

        if (sidebarWidth + principalViewMinimumWidth + inspectorWidth) > windowWidth {
            if let collapsedPanelOverride = model.collapsedPanelOverride {
                switch collapsedPanelOverride {
                case .sidebar:
                    splitViewController.itemA.isCollapsed = true
                case .inspector:
                    splitViewController.itemC.isCollapsed = true
                }
            } else {
                // if no overrides, then hide the inspector first.
                splitViewController.itemA.isCollapsed = model.sidebarPrefersCollapsed
                splitViewController.itemC.isCollapsed = true
            }
        } else {
            splitViewController.itemA.isCollapsed = model.sidebarPrefersCollapsed
            splitViewController.itemC.isCollapsed = model.inspectorPrefersCollapsed
        }
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

extension NewMainViewController: NSMenuDelegate {
    func menuWillOpen(_ menu: NSMenu) {
        guard menu.identifier == .sortListItemMenu else { return }
        for item in menu.items {
            item.state = (model.dockerSortingMethod.rawValue == item.tag) ? .on : .off
        }
    }
}
