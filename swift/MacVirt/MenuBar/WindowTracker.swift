//
// Created by Danny Lin on 5/20/23.
//

import AppKit
import Combine
import Defaults
import Foundation
import SwiftUI

// helps for e.g. onboarding flow, when we sometimes momentarily have no windows
private let policyDebounce = 0.1

private class FuncDebounce {
    private let duration: TimeInterval

    private var timer: Timer?

    init(duration: TimeInterval) {
        self.duration = duration
    }

    func call(fn: @escaping () -> Void) {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: duration, repeats: false) { _ in
            fn()
        }
    }

    func cancel() {
        timer?.invalidate()
    }
}

@MainActor
class WindowTracker: ObservableObject {
    private var cancellables = Set<AnyCancellable>()
    private let setPolicyDebounce = FuncDebounce(duration: policyDebounce)

    var openDockerLogWindowIds: Set<DockerContainerId> = []
    var openK8sLogWindowIds: Set<K8SResourceId> = []

    // TODO: fix reference cycle
    var menuBar: MenuBarController?

    func updateState() {
        let newPolicy = derivePolicy()
        setPolicyDebounce.call { [self] in
            setPolicy(newPolicy)
        }
    }

    private func derivePolicy() -> NSApplication.ActivationPolicy {
        // if no menu bar app, always act like normal
        if !Defaults[.globalShowMenubarExtra] {
            return .regular
        }

        // this is for when we have a menu bar app:
        // check windows
        let windowCount =
            NSApp.windows
            .filter { $0.isUserFacing }
            .count
        if windowCount == 0 {
            return .accessory
        } else {
            return .regular
        }
    }

    func setPolicy(_ newPolicy: NSApplication.ActivationPolicy) {
        let currentPolicy = NSApp.activationPolicy()
        if newPolicy != currentPolicy {
            NSLog("changing policy from \(currentPolicy) to \(newPolicy)")
            NSApp.setActivationPolicy(newPolicy)

            // activate if -> regular
            if newPolicy == .regular {
                NSApp.activate(ignoringOtherApps: true)
            }

            // hide if -> accessory
            if newPolicy == .accessory {
                menuBar?.onTransitionToBackground()

                // don't hide - breaks popover animation, and not necessary
                NSApp.deactivate()
            }
        }
    }
}
