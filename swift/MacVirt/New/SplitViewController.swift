//
//  SplitViewController.swift
//  MacVirt
//
//  Created by Andrew Zheng (github.com/aheze) on 12/18/23.
//  Copyright © 2023 Andrew Zheng. All rights reserved.
//

import AppKit

enum SplitType: String {
    case doublePane = "Double"
    case triplePane = "Triple"
}

class SplitViewController: NSSplitViewController {
    let vcA = SidebarViewController()
    let vcB = PrincipalViewController()
    let vcC = InspectorViewController()

    lazy var itemA = NSSplitViewItem(sidebarWithViewController: vcA)
    lazy var itemB = NSSplitViewItem(contentListWithViewController: vcB)
    lazy var itemC = NSSplitViewItem(viewController: vcC)

    var tab: NavTabId? = nil
    var splitType: SplitType? {
        if let tab {
            if tab == .activityMonitor || tab == .cli {
                return .doublePane
            } else {
                return .triplePane
            }
        } else {
            return nil
        }
    }

    init() {
        super.init(nibName: nil, bundle: nil)

        itemA.minimumThickness = 160
        itemA.maximumThickness = 250
        itemA.preferredThicknessFraction = 0.2
        itemA.holdingPriority = .defaultHigh

        itemB.minimumThickness = 200
        itemB.preferredThicknessFraction = 0.3

        itemC.minimumThickness = 300
        itemC.preferredThicknessFraction = 0.5

        addSplitViewItem(itemA)
        addSplitViewItem(itemB)
        addSplitViewItem(itemC)

        // initial positions
        splitView.setPosition(160, ofDividerAt: 0)
        splitView.setPosition(160+300, ofDividerAt: 1)
    }

    required init?(coder: NSCoder) {
        super.init(coder: coder)
    }

    func setOnTabChange(_ onTabChange: @escaping (NavTabId) -> Void) {
        vcB.onTabChange = { [weak self] tab in
            guard let self else { return }
            onTabChange(tab)

            self.tab = tab
            itemC.isCollapsed = splitType == .doublePane
            updateAutosaveName()
        }
    }
    
    override func viewWillAppear() {
        updateAutosaveName()
    }

    func updateAutosaveName() {
        if let windowId = splitView.window?.identifier?.rawValue, let splitType {
            splitView.autosaveName = "\(windowId) : \(splitType) : SplitViewController3"
        }
    }
}
