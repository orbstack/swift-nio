//
// Created by Danny Lin on 5/20/23.
//

import Foundation
import SwiftUI
import AppKit
import Defaults
import Combine

class WindowTracker: ObservableObject {
    private var lastPolicy = NSApplication.ActivationPolicy.regular
    private var cancelables = Set<AnyCancellable>()

    init() {
        // monitor close notifications
        // no equivalent for open, so rely on SwiftUI .onAppear callbacks
        NotificationCenter.default
                // nil = all windows
                .publisher(for: NSWindow.willCloseNotification, object: nil)
                .sink { [weak self] notification in
                    guard let self = self else { return }
                    self.onWindowDisappear(closingWindow: notification.object as? NSWindow)
                }
                .store(in: &cancelables)
    }

    func onWindowAppear() {
        updateState(isWindowAppearing: true)
    }

    private func onWindowDisappear(closingWindow: NSWindow?) {
        updateState(closingWindow: closingWindow)
    }

    private func updateState(closingWindow: NSWindow? = nil, isWindowAppearing: Bool = false) {
        let newPolicy = derivePolicy(closingWindow: closingWindow, isWindowAppearing: isWindowAppearing)
        if newPolicy != lastPolicy {
            NSLog("changing policy from \(lastPolicy) to \(newPolicy)")
            NSApp.setActivationPolicy(newPolicy)
            lastPolicy = newPolicy

            // activate if -> regular
            if newPolicy == .regular {
                NSApp.activate(ignoringOtherApps: true)

                // also schedule a task to make sure new window is key
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    // find first userFacing window
                    let window = NSApp.windows.first { $0.isUserFacing }
                    window?.makeKeyAndOrderFront(nil)
                }
            }

            // hide if -> accessory
            if newPolicy == .accessory {
                NSApp.hide(nil)
            }
        }
    }

    private func derivePolicy(closingWindow: NSWindow?, isWindowAppearing: Bool) -> NSApplication.ActivationPolicy {
        // if no menu bar app, always act like normal
        // TODO setting
        if !Defaults[.globalShowMenubarExtra] {
            return .regular
        }

        // this is for when we have a menu bar app:
        // check windows
        let windowCount = NSApp.windows
                .filter { $0.isUserFacing && $0 != closingWindow }
                // onAppear is *before* window created
                .count + (isWindowAppearing ? 1 : 0)
        if windowCount == 0 {
            return .accessory
        } else {
            return .regular
        }
    }
}