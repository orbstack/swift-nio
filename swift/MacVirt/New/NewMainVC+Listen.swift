//
//  NewMainVC+Listen.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/11/23.
//

import Carbon.HIToolbox // for keyboard shortcuts
import SwiftUI

extension NewMainViewController {
    func listen() {
        model.$selection
            .sink { [weak self] selection in
                guard let self else { return }
                self.updateToolbarFromSelectionChange(toolbarIdentifier: selection)
            }
            .store(in: &cancellables)

        // keyboard shortcuts
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event -> NSEvent? in
            guard let self else { return event }

            if event.modifierFlags.contains(.command) {
                switch Int(event.keyCode) {
                case kVK_ANSI_N:
                    switch self.model.selection {
                    case .volumes:
                        self.volumesPlusButton(nil)
                    case .machines:
                        self.machinesPlusButton(nil)
                    default:
                        break
                    }
                case kVK_ANSI_O:
                    switch self.model.selection {
                    case .images:
                        self.imagesFolderButton(nil)
                    case .volumes:
                        self.volumesFolderButton(nil)
                    default:
                        break
                    }
                default:
                    break
                }
            }

            return event
        }

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
