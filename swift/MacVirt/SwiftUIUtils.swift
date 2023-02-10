//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import SwiftUI
import Combine

// this new impl from https://stackoverflow.com/a/72676028, combined with weak WindowHolder, fixes duplicate windows opening on alert
// but still not 100% reliable
struct WindowAccessor: NSViewRepresentable {
    let holder: WindowHolder

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        context.coordinator.monitorView(view)
        return view
    }

    func updateNSView(_ view: NSView, context: Context) {
    }

    func makeCoordinator() -> WindowMonitor {
        WindowMonitor {
            holder.window = $0
        }
    }

    class WindowMonitor: NSObject {
        private var cancellables = Set<AnyCancellable>()
        private var onChange: (NSWindow?) -> Void

        init(_ onChange: @escaping (NSWindow?) -> Void) {
            self.onChange = onChange
        }

        /// This function uses KVO to observe the `window` property of `view` and calls `onChange()`
        func monitorView(_ view: NSView) {
            view.publisher(for: \.window)
                    .removeDuplicates()
                    .dropFirst()
                    .sink { [weak self] newWindow in
                        guard let self = self else { return }
                        self.onChange(newWindow)
                        if let newWindow = newWindow {
                            self.monitorClosing(of: newWindow)
                        }
                    }
                    .store(in: &cancellables)
        }

        /// This function uses notifications to track closing of `window`
        private func monitorClosing(of window: NSWindow) {
            NotificationCenter.default
                    .publisher(for: NSWindow.willCloseNotification, object: window)
                    .sink { [weak self] notification in
                        guard let self = self else { return }
                        self.onChange(nil)
                        self.cancellables.removeAll()
                    }
                    .store(in: &cancellables)
        }
    }
}

class WindowHolder: ObservableObject {
    weak var window: NSWindow?
}