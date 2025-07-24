//
//  NewMainVC+Listen.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/11/23.
//

import SwiftUI

extension NewMainViewController {
    func listen() {
        splitViewController.setOnTabChange { [weak self] tab in
            guard let self else { return }
            self.updateToolbarFromSelectionChange(toolbarIdentifier: tab)
        }

        model.menuActionRouter.sink { [weak self] router in
            guard let self else { return }

            switch router {
            case .newContainer:
                self.actionDockerContainersNew(nil)
            case .openContainers:
                self.actionDockerContainersOpen(nil)
            case .openContainerInNewWindow:
                self.actionDockerContainersOpenWindow(nil)
            case .newVolume:
                self.actionDockerVolumesNew(nil)
            case .openVolumes:
                self.actionDockerVolumesOpen(nil)
            case .openImages:
                self.actionDockerImagesOpen(nil)
            case .importMachine:
                self.actionMachinesImport(nil)
            case .importVolume:
                self.actionDockerVolumesImport(nil)
            case .importImage:
                self.actionDockerImagesImport(nil)
            case .newMachine:
                self.actionMachinesNew(nil)
            case .newNetwork:
                self.actionDockerNetworksNew(nil)
            }
        }
        .store(in: &cancellables)
    }

    func updateToolbarFromSelectionChange(toolbarIdentifier: NavTabId) {
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
