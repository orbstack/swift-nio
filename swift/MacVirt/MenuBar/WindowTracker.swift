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
        print("appearing")
        updateState(isWindowAppearing: true)
    }

    private func onWindowDisappear(closingWindow: NSWindow?) {
        print("disappearing")
        updateState(closingWindow: closingWindow)
    }

    private func updateState(closingWindow: NSWindow? = nil, isWindowAppearing: Bool = false) {
        let newPolicy = derivePolicy(closingWindow: closingWindow, isWindowAppearing: isWindowAppearing)
        setPolicy(newPolicy)
    }

    private func derivePolicy(closingWindow: NSWindow?, isWindowAppearing: Bool) -> NSApplication.ActivationPolicy {
        // if no menu bar app, always act like normal
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

    func setPolicy(_ newPolicy: NSApplication.ActivationPolicy) {
        if newPolicy != lastPolicy {
            NSLog("changing policy from \(lastPolicy) to \(newPolicy)")
            NSApp.setActivationPolicy(newPolicy)
            lastPolicy = newPolicy

            // activate if -> regular
            if newPolicy == .regular {
                // workaround for app menu bar not working after user reopens app from Dock
                DispatchQueue.main.asyncAfter(deadline: .now() + .milliseconds(250)) {
                    // only activate after workaround applied
                    NSApp.activate(ignoringOtherApps: true)
                }

                // also make sure new window is key
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    // find first userFacing window
                    let window = NSApp.windows.first { $0.isUserFacing }
                    window?.makeKeyAndOrderFront(nil)
                }
            }

            // hide if -> accessory
            if newPolicy == .accessory {
                NSApp.hide(nil)
                NSApp.deactivate()
            }
        }
    }
}
