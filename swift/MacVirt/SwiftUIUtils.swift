//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import SwiftUI
import Combine
import CoreGraphics

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

func rectReader(_ binding: Binding<CGRect>, _ space: CoordinateSpace = .global) -> some View {
    GeometryReader { (geometry) -> Color in
        let rect = geometry.frame(in: space)
        DispatchQueue.main.async {
            if rect != binding.wrappedValue {
                binding.wrappedValue = rect
            }
        }
        return .clear
    }
}

extension CGKeyCode {
    static let kVK_Option     : CGKeyCode = 0x3A
    static let kVK_RightOption: CGKeyCode = 0x3D

    var isPressed: Bool {
        CGEventSource.keyState(.combinedSessionState, key: self)
    }

    static var optionKeyPressed: Bool {
        return Self.kVK_Option.isPressed || Self.kVK_RightOption.isPressed
    }
}

struct SystemColors {
    private static let all = [
        Color(.systemRed),
        Color(.systemGreen),
        Color(.systemBlue),
        Color(.systemOrange),
        Color(.systemYellow),
        Color(.systemBrown),
        Color(.systemPink),
        Color(.systemPurple),
        Color(.systemGray),
        Color(.systemTeal),
        Color(.systemIndigo),
        Color(.systemMint),
        Color(.systemCyan),
    ]

    static func forHashable(_ hashable: AnyHashable) -> Color {
        let index = hashable.hashValue %% all.count
        return all[index]
    }
}