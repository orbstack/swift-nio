//
//  NewMainViewController.swift
//  MacVirt
//
//  Created by Andrew Zheng on 11/23/23.
//

import AppKit
import Combine
import SwiftUI

enum Panel {
    case sidebar
    case inspector
}

class NewMainViewController: NSViewController {
    var model: VmViewModel

    var horizontalConstraint: NSLayoutConstraint!
    var verticalConstraint: NSLayoutConstraint!

    let splitViewController = SplitViewController()

    var cancellables = Set<AnyCancellable>()
    var keyMonitor: Any?

    // MARK: - Toolbar

    var toolbar = NSToolbar(identifier: NewToolbarIdentifier.containers.rawValue)

    lazy var toggleSidebarButton = makeToolbarItem(
        itemIdentifier: .toggleSidebarButton,
        icon: "sidebar.left",
        title: "Toggle Sidebar",
        action: #selector(toggleSidebarButton),
        isEnabledFollowsModelState: false
    )

    lazy var toggleInspectorButton = makeToolbarItem(
        itemIdentifier: .toggleInspectorButton,
        icon: "sidebar.right",
        title: "Toggle Inspector",
        action: #selector(toggleInspectorButton),
        isEnabledFollowsModelState: false
    )

    lazy var containersFilterMenu = {
        let menuItem1 = NSMenuItem(title: "Show stopped containers", action: #selector(containersFilterMenu1), keyEquivalent: "")
        menuItem1.target = self

        let item = makeMenuToolbarItem(
            itemIdentifier: .containersFilterMenu,
            icon: "line.3.horizontal.circle",
            title: "Filter Containers",
            menuItems: [menuItem1]
        )

        model.$dockerFilterShowStopped.sink { [weak menuItem1] on in
            menuItem1?.state = on ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var volumesFolderButton = {
        let item = makeToolbarItem(
            itemIdentifier: .volumesFolderButton,
            icon: "folder",
            title: "Open Volumes",
            action: #selector(volumesFolderButton)
        )
        return item
    }()

    lazy var volumesPlusButton = {
        let item = makeToolbarItem(
            itemIdentifier: .volumesPlusButton,
            icon: "plus",
            title: "New Volume",
            action: #selector(volumesPlusButton)
        )
        return item
    }()

    lazy var imagesFolderButton = {
        let item = makeToolbarItem(
            itemIdentifier: .imagesFolderButton,
            icon: "folder",
            title: "Open Images",
            action: #selector(imagesFolderButton)
        )
        return item
    }()

    lazy var podsStartToggle = {
        let item = NSToolbarItem(itemIdentifier: .podsStartToggle)
        let toggle = NSSwitch()
        toggle.target = self
        toggle.action = #selector(podsStartToggle)
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
        let menuItem1 = NSMenuItem(title: "Show system namespace", action: #selector(podsFilterMenu1), keyEquivalent: "")
        menuItem1.target = self

        let item = makeMenuToolbarItem(
            itemIdentifier: .podsFilterMenu,
            icon: "line.3.horizontal.circle",
            title: "Filter",
            menuItems: [menuItem1]
        )

        model.$k8sFilterShowSystemNs.sink { [weak menuItem1] on in
            menuItem1?.state = on ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var servicesFilterMenu = {
        let menuItem1 = NSMenuItem(title: "Show system namespace", action: #selector(servicesFilterMenu1), keyEquivalent: "")
        menuItem1.target = self

        menuItem1.state = .on

        let item = makeMenuToolbarItem(
            itemIdentifier: .servicesFilterMenu,
            icon: "line.3.horizontal.circle",
            title: "Filter",
            menuItems: [menuItem1]
        )

        model.$k8sFilterShowSystemNs.sink { [weak menuItem1] on in
            menuItem1?.state = on ? .on : .off
        }.store(in: &cancellables)

        return item
    }()

    lazy var machinesPlusButton = {
        let item = makeToolbarItem(
            itemIdentifier: .machinesPlusButton,
            icon: "plus",
            title: "New Machine",
            action: #selector(machinesPlusButton)
        )
        return item
    }()

    lazy var commandsHelpButton = {
        let item = makeToolbarItem(
            itemIdentifier: .commandsHelpButton,
            icon: "questionmark.circle",
            title: "Go to Docs",
            action: #selector(commandsHelpButton)
        )
        return item
    }()

    lazy var searchItem = {
        let item = NSSearchToolbarItem(itemIdentifier: .searchItem)
        item.searchField.delegate = self
        return item
    }()

    // MARK: - Init

    init(model: VmViewModel) {
        self.model = model
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
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
        isEnabledFollowsModelState: Bool = true
    ) -> NSToolbarItem {
        let item = NSToolbarItem(itemIdentifier: itemIdentifier)
        let image = NSImage(systemSymbolName: icon, accessibilityDescription: nil)!
        item.image = image
        item.isBordered = true
        item.target = self
        item.action = action
        item.label = title // won't be shown actually because toolbar is `.iconOnly`
        item.toolTip = title

        if isEnabledFollowsModelState {
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
        isEnabledFollowsModelState: Bool = true,
        menuItems: [NSMenuItem]
    ) -> NSMenuToolbarItem {
        let menu = NSMenu(title: title)
        menu.items = menuItems

        let item = NSMenuToolbarItem(itemIdentifier: itemIdentifier)
        item.menu = menu
        item.image = NSImage(systemSymbolName: icon, accessibilityDescription: nil)!
        item.isBordered = true
        item.toolTip = title

        if isEnabledFollowsModelState {
            model.$state.sink { [weak item] state in
                item?.isEnabled = state == .running
            }.store(in: &cancellables)
        }

        return item
    }
}
