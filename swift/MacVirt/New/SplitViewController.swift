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
    lazy var itemB = NSSplitViewItem(viewController: vcB)
    lazy var itemC = NSSplitViewItem(inspectorWithViewController: vcC)

    var itemACurrentlyCollapsed = false
    var itemCCurrentlyCollapsed = false
    var lastKnownWidth = CGFloat.zero
    var userGestureCollapsedPanel: ((Panel) -> Void)?
    var userGestureExpandedPanel: ((Panel) -> Void)?

    init() {
        super.init(nibName: nil, bundle: nil)

        addSplitViewItem(itemA)
        addSplitViewItem(itemB)
        addSplitViewItem(itemC)

        itemA.minimumThickness = 165
        itemA.maximumThickness = 250

        itemB.minimumThickness = 250

        itemC.minimumThickness = 280
        itemC.maximumThickness = 600

        if #available(macOS 14.0, *) {
            itemC.allowsFullHeightLayout = true
        } else {
            itemC.allowsFullHeightLayout = false
        }

        itemA.canCollapseFromWindowResize = false
    }

    required init?(coder: NSCoder) {
        super.init(coder: coder)
    }

    // this gets called both when we manually set `isCollapsed` on window resize,
    // and also when the user drags to hide the panel.
    // we differentiate between these two cases by checking if the window width is the same.
    override func splitViewDidResizeSubviews(_: Notification) {
        if itemA.isCollapsed != itemACurrentlyCollapsed, lastKnownWidth == view.bounds.width {
            if itemA.isCollapsed {
                userGestureCollapsedPanel?(.sidebar)
            } else {
                userGestureExpandedPanel?(.sidebar)
            }
        }

        if itemC.isCollapsed != itemCCurrentlyCollapsed, lastKnownWidth == view.bounds.width {
            if itemC.isCollapsed {
                userGestureCollapsedPanel?(.inspector)
            } else {
                userGestureExpandedPanel?(.inspector)
            }
        }

        itemACurrentlyCollapsed = itemA.isCollapsed
        itemCCurrentlyCollapsed = itemC.isCollapsed
        lastKnownWidth = view.bounds.width
    }

    func setOnTabChange(_ onTabChange: @escaping (NavTabId) -> Void) {
        vcB.onTabChange = onTabChange
    }
}
