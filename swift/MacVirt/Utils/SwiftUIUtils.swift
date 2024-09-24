//
// Created by Danny Lin on 2/8/23.
//

import Combine
import CoreGraphics
import Foundation
import SwiftUI

// this new impl from https://stackoverflow.com/a/72676028, combined with weak WindowHolder, fixes duplicate windows opening on alert
// but still not 100% reliable
struct WindowAccessor: NSViewRepresentable {
    let holder: WindowHolder

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        context.coordinator.monitorView(view)
        return view
    }

    func updateNSView(_: NSView, context: Context) {
        context.coordinator.holder = holder
    }

    func makeCoordinator() -> WindowMonitor {
        WindowMonitor(holder: holder)
    }

    class WindowMonitor: NSObject {
        private var cancellables = Set<AnyCancellable>()
        var holder: WindowHolder

        init(holder: WindowHolder) {
            self.holder = holder
        }

        /// This function uses KVO to observe the `window` property of `view` and calls `onChange()`
        func monitorView(_ view: NSView) {
            view.publisher(for: \.window)
                .removeDuplicates()
                .dropFirst()
                .sink { [weak self] newWindow in
                    // publishing within view update is UB
                    DispatchQueue.main.async {
                        self?.holder.windowRef = WindowRef(window: newWindow)
                    }
                }
                .store(in: &cancellables)
        }
    }
}

struct WindowRef {
    weak var window: NSWindow?
}

class WindowHolder: ObservableObject {
    // wrapped object can't be weak
    // need Published so that onChange(of: windowHolder.window) works
    @Published fileprivate var windowRef = WindowRef()

    var window: NSWindow? {
        windowRef.window
    }
}

func rectReader(_ binding: Binding<CGRect>, _ space: CoordinateSpace = .global) -> some View {
    GeometryReader { geometry -> Color in
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
    static let kVK_Option: CGKeyCode = 0x3A
    static let kVK_RightOption: CGKeyCode = 0x3D

    var isPressed: Bool {
        CGEventSource.keyState(.combinedSessionState, key: self)
    }

    static var optionKeyPressed: Bool {
        return kVK_Option.isPressed || kVK_RightOption.isPressed
    }
}

enum SystemColors {
    private static let all = [
        // removed due to semantic meaning
        // Color(.systemRed),
        Color(.systemGreen),
        Color(.systemBlue),
        // removed: semantic, could be confusing in k8s case
        // Color(.systemOrange),
        // removed due to poor contrast on light
        // Color(.systemYellow),
        Color(.systemBrown),
        // removed: too close to red
        // Color(.systemPink),
        Color(.systemPurple),
        Color(.systemGray),
        Color(.systemTeal),
        Color(.systemIndigo),
        Color(.systemMint),
        // removed: poor contrast, too bright in dark
        // Color(.systemCyan),
    ]

    static func forString(_ str: String) -> Color {
        let index = Int(stableStringHash(str)) %% all.count
        // tone down saturation
        return desaturate(all[index])
    }

    static func desaturate(_ color: Color) -> Color {
        color.opacity(0.8)
    }
}

// Swift default hashable hashValue is keyed randomly on start
private func stableStringHash(_ str: String) -> UInt64 {
    var result = UInt64(5381)
    let buf = [UInt8](str.utf8)
    for b in buf {
        result = 127 * (result & 0x00FF_FFFF_FFFF_FFFF) + UInt64(b)
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
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 08) & 0xFF) / 255,
            blue: Double((hex >> 00) & 0xFF) / 255,
            opacity: alpha
        )
    }
}

extension NSColor {
    convenience init(hex: UInt, alpha: Double = 1) {
        self.init(
            srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 08) & 0xFF) / 255,
            blue: CGFloat((hex >> 00) & 0xFF) / 255,
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

extension Text {
    func textSelectionWithWorkaround() -> some View {
        // WA: selecting text in dark mode changes color to black when on material bg
        foregroundColor(.primary)
            .textSelection(.enabled)
    }
}

extension Slider {
    init<V: BinaryInteger>(value: Binding<V>, in bounds: ClosedRange<V>, step: V = 1, @ViewBuilder label: () -> Label, @ViewBuilder minimumValueLabel: () -> ValueLabel, @ViewBuilder maximumValueLabel: () -> ValueLabel, onEditingChanged: @escaping (Bool) -> Void = { _ in }) {
        let binding = Binding<Double>(
            get: { Double(value.wrappedValue) },
            set: { value.wrappedValue = V($0) }
        )

        self.init(value: binding,
                  in: Double(bounds.lowerBound) ... Double(bounds.upperBound),
                  step: Double(step),
                  label: label,
                  minimumValueLabel: minimumValueLabel,
                  maximumValueLabel: maximumValueLabel,
                  onEditingChanged: onEditingChanged)
    }
}

extension NSWorkspace {
    static func openSubwindow(_ path: String) {
        NSWorkspace.shared.open(URL(string: "orbstack://\(path)")!)
    }

    static func openFolder(_ path: String) {
        NSWorkspace.shared.open(URL(fileURLWithPath: path, isDirectory: true))
    }
}

extension HorizontalAlignment {
    private struct TableColumnAlignment: AlignmentID {
        static func defaultValue(in context: ViewDimensions) -> CGFloat {
            context[HorizontalAlignment.center]
        }
    }

    static let tableColumnAlignmentGuide = HorizontalAlignment(TableColumnAlignment.self)
}

// TODO: use preference to propagate up from SimpleKvTableRow... but how to measure? need multi-pass layout
// but this will break if we localize (diff langs will have diff longest labels... depends on font too)
private struct LongestLabelKey: EnvironmentKey {
    static let defaultValue = ""
}

extension EnvironmentValues {
    var longestLabelKey: String {
        get { self[LongestLabelKey.self] }
        set { self[LongestLabelKey.self] = newValue }
    }
}

// can't use Grid(13) or Table(14 for tableColumnsVisible) due to macOS version req
// this version is terribly hacky, doesn't use alignmentGuide
// because alignmentGuide doesn't respect sidebar maxWidth in inspector
struct SimpleKvTable<Content: View>: View {
    private let spacing: CGFloat
    private let longestLabel: String
    @ViewBuilder private let content: () -> Content

    init(spacing: CGFloat = 4, longestLabel: String, @ViewBuilder content: @escaping () -> Content) {
        self.spacing = spacing
        self.longestLabel = longestLabel
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: spacing) {
            content()
        }
        .environment(\.longestLabelKey, longestLabel)
    }
}

struct SimpleKvTableRow<Content: View>: View {
    private let label: String
    private let lineLimit: Int?
    @ViewBuilder private let content: () -> Content

    @Environment(\.longestLabelKey) private var longestLabel

    init(_ label: String, lineLimit: Int? = 1, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.lineLimit = lineLimit
        self.content = content
    }

    var body: some View {
        HStack(alignment: .top) {
            ZStack(alignment: .trailing) {
                Text(longestLabel)
                    .fontWeight(.medium)
                    .frame(maxHeight: 1)
                    .opacity(0)
                    .accessibility(hidden: true)

                Text(label)
                    .fontWeight(.medium)
            }

            content()
                .lineLimit(lineLimit)
        }
    }
}

// can't use Grid(13) or Table(14 for tableColumnsVisible) due to macOS version req
// this is the alignmentGuide version, which is OK to use if it's in a ScrollView
struct AlignedSimpleKvTable<Content: View>: View {
    private let spacing: CGFloat
    @ViewBuilder private let content: () -> Content

    init(spacing: CGFloat = 4, @ViewBuilder content: @escaping () -> Content) {
        self.spacing = spacing
        self.content = content
    }

    var body: some View {
        VStack(alignment: .tableColumnAlignmentGuide, spacing: spacing) {
            content()
        }
    }
}

struct AlignedSimpleKvTableRow<Content: View>: View {
    private let label: String
    private let lineLimit: Int?
    @ViewBuilder private let content: () -> Content

    init(_ label: String, lineLimit: Int? = 1, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.lineLimit = lineLimit
        self.content = content
    }

    var body: some View {
        HStack(alignment: .top) {
            Text(label)
                .fontWeight(.medium)
                .alignmentGuide(.tableColumnAlignmentGuide) { context in
                    context[.trailing]
                }

            content()
                .lineLimit(lineLimit)
        }
    }
}
