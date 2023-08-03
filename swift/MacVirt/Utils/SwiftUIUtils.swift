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
        // removed due to semantic meaning
        //Color(.systemRed),
        Color(.systemGreen),
        Color(.systemBlue),
        Color(.systemOrange),
        // removed due to poor contrast on light
        //Color(.systemYellow),
        Color(.systemBrown),
        // removed: too close to red
        //Color(.systemPink),
        Color(.systemPurple),
        Color(.systemGray),
        Color(.systemTeal),
        Color(.systemIndigo),
        Color(.systemMint),
        // removed: poor contrast, too bright in dark
        //Color(.systemCyan),
    ]

    static func forString(_ str: String) -> Color {
        let index = Int(stableStringHash(str)) %% all.count
        // tone down saturation
        return all[index].opacity(0.8)
    }
}

// Swift default hashable hashValue is keyed randomly on start
private func stableStringHash(_ str: String) -> UInt64 {
    var result = UInt64(5381)
    let buf = [UInt8](str.utf8)
    for b in buf {
        result = 127 * (result & 0x00ffffffffffffff) + UInt64(b)
    }
    return result
}

extension View {
    func border(width: CGFloat, edges: [Edge], color: Color) -> some View {
        overlay(EdgeBorder(width: width, edges: edges).foregroundColor(color))
    }
}

struct EdgeBorder: Shape {
    var width: CGFloat
    var edges: [Edge]

    func path(in rect: CGRect) -> Path {
        var path = Path()
        for edge in edges {
            var x: CGFloat {
                switch edge {
                case .top, .bottom, .leading: return rect.minX
                case .trailing: return rect.maxX - width
                }
            }

            var y: CGFloat {
                switch edge {
                case .top, .leading, .trailing: return rect.minY
                case .bottom: return rect.maxY - width
                }
            }

            var w: CGFloat {
                switch edge {
                case .top, .bottom: return rect.width
                case .leading, .trailing: return width
                }
            }

            var h: CGFloat {
                switch edge {
                case .top, .bottom: return width
                case .leading, .trailing: return rect.height
                }
            }
            path.addRect(CGRect(x: x, y: y, width: w, height: h))
        }
        return path
    }
}

extension NSWindow {
    var isUserFacing: Bool {
        // this ignores menu and status item windows
        // need isVisible check - SwiftUI windows are lazy destroyed after close
        styleMask.contains(.titled) && (isVisible || isMiniaturized)
    }
}

extension View {
    /// Applies the given transform if the given condition evaluates to `true`.
    /// - Parameters:
    ///   - condition: The condition to evaluate.
    ///   - transform: The transform to apply to the source `View`.
    /// - Returns: Either the original `View` or the modified `View` if the condition is `true`.
    @ViewBuilder func `if`<Content: View>(_ condition: Bool, transform: (Self) -> Content) -> some View {
        if condition {
            transform(self)
        } else {
            self
        }
    }
}

extension Color {
    init(hex: UInt, alpha: Double = 1) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xff) / 255,
            green: Double((hex >> 08) & 0xff) / 255,
            blue: Double((hex >> 00) & 0xff) / 255,
            opacity: alpha
        )
    }
}

extension NSColor {
    convenience init(hex: UInt, alpha: Double = 1) {
        self.init(
            srgbRed: CGFloat((hex >> 16) & 0xff) / 255,
            green: CGFloat((hex >> 08) & 0xff) / 255,
            blue: CGFloat((hex >> 00) & 0xff) / 255,
            alpha: CGFloat(alpha)
        )
    }
}

struct ToggleSidebarButton: View {
    var body: some View {
        Button {
            NSApp.sendAction(#selector(NSSplitViewController.toggleSidebar(_:)), to: nil, from: nil)
        } label: {
            Label("Toggle Sidebar", systemImage: "sidebar.leading")
        }
        .help("Toggle Sidebar")
    }
}