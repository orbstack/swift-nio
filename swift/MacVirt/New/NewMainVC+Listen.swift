//
//  NewMainVC+Listen.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/11/23.
//

import SwiftUI

extension NewMainViewController {
    func listen() {
        model.$selection
            .sink { [weak self] selection in
                guard let self else { return }
                self.updateToolbarFromSelectionChange(toolbarIdentifier: selection)
            }
            .store(in: &cancellables)

        model.menuActionRouter.sink { [weak self] router in
            guard let self else { return }

            switch router {
            case .newVolume:
                self.volumesPlusButton(nil)
            case .openVolumes:
                self.volumesFolderButton(nil)
            case .openImages:
                self.imagesFolderButton(nil)
            case .newMachine:
                self.machinesPlusButton(nil)
            }
        }
        .store(in: &cancellables)

        splitViewController.userGestureCollapsedPanel = { [weak self] panel in
            guard let self else { return }
            switch panel {
            case .sidebar:
                didCollapseSidebar()
            case .inspector:
                didCollapseInspector()
            }
        }
        splitViewController.userGestureExpandedPanel = { [weak self] panel in
            guard let self else { return }
            switch panel {
            case .sidebar:
                didExpandSidebar()
            case .inspector:
                didExpandInspector()
            }
        }
    }

    func updateToolbarFromSelectionChange(toolbarIdentifier: NewToolbarIdentifier) {
        let toolbar = NSToolbar(identifier: toolbarIdentifier.rawValue)
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly

        self.toolbar = toolbar

        // clear the search bar
        searchItem.searchField.stringValue = ""
        model.searchText = ""

        // window will be nil on launch,
        // but we'll also do `window.toolbar = toolbar` in `movedToWindow`
        // so it's fine.
        if let window = view.window {
            window.toolbar = toolbar
        }
    }
}

// Search delegate
extension NewMainViewController: NSSearchFieldDelegate {
    func controlTextDidChange(_ obj: Notification) {
        guard let searchField = obj.object as? NSSearchField else { return }
        model.searchText = searchField.stringValue
    }
}
