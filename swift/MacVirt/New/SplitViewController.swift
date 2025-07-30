//
//  SplitViewController.swift
//  MacVirt
//
//  Created by Andrew Zheng (github.com/aheze) on 12/18/23.
//  Copyright © 2023 Andrew Zheng. All rights reserved.
//

import AppKit

class SplitViewController: NSSplitViewController {
    let vcA = SidebarViewController()
    let vcB = PrincipalViewController()
    let vcC = InspectorViewController()

    lazy var itemA = NSSplitViewItem(sidebarWithViewController: vcA)
    lazy var itemB = NSSplitViewItem(contentListWithViewController: vcB)
    lazy var itemC = NSSplitViewItem(viewController: vcC)

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

            // these tabs don't have an inspector
            if tab == .activityMonitor || tab == .cli {
                itemC.isCollapsed = true
            } else {
                itemC.isCollapsed = false
            }
        }
    }
    
    override func viewWillAppear() {
        if let windowId = splitView.window?.identifier?.rawValue {
            // new save ID after changing to master-detail layout
            splitView.autosaveName = "\(windowId) : SplitViewController2"
        }
    }
}
