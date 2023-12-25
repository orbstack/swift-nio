//
//  NewMainVC+SplitView.swift
//  MacVirt
//
//  Created by Andrew Zheng (github.com/aheze) on 12/18/23.
//  Copyright © 2023 Andrew Zheng. All rights reserved.
//

import AppKit

// the split view controller calls these functions when the sidebar/inspector
// is collapsed/hidden by the user (via button press or drag)
extension NewMainViewController {
    func didCollapseSidebar() {
        model.sidebarPrefersCollapsed = true
        model.collapsedPanelOverride = nil
    }

    func didExpandSidebar() {
        // if the sidebar is expanded, then...
        model.sidebarPrefersCollapsed = false

        let windowWidth = view.bounds.size.width
        let sidebarWidth = splitViewController.itemA.viewController.view.bounds.width
        let inspectorWidth = splitViewController.itemC.viewController.view.bounds.width
        if (sidebarWidth + principalViewMinimumWidth + inspectorWidth) > windowWidth {
            splitViewController.itemC.animator().isCollapsed = true

            // opened up the sidebar when the window is too small
            model.collapsedPanelOverride = .inspector
        } else {
            model.collapsedPanelOverride = nil
        }
    }

    func didCollapseInspector() {
        model.inspectorPrefersCollapsed = true
        model.collapsedPanelOverride = nil
    }

    func didExpandInspector() {
        model.inspectorPrefersCollapsed = false

        let windowWidth = view.bounds.size.width
        let sidebarWidth = splitViewController.itemA.viewController.view.bounds.width
        let inspectorWidth = splitViewController.itemC.viewController.view.bounds.width
        if (sidebarWidth + principalViewMinimumWidth + inspectorWidth) > windowWidth {
            splitViewController.itemA.animator().isCollapsed = true

            model.collapsedPanelOverride = .sidebar
        } else {
            model.collapsedPanelOverride = nil
        }
    }
}
