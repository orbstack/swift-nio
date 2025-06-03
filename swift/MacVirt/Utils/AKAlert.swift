//
// Created by Danny Lin on 3/7/24.
//

import AppKit
import Foundation
import SwiftUI

private let MAX_INLINE_DESC_CHARS = 500

struct AKAlert<T: Equatable>: ViewModifier {
    @Binding var presentedData: T?
    let style: NSAlert.Style
    @StateObject private var windowHolder = WindowHolder()
    // prevent duplicate queued modal from onChange(of: presentedData) and onChange(of: window)
    @State private var presented = false
    @AKAlertBuilder let contentBuilder: (T) -> AKAlertBody

    func body(content: Content) -> some View {
        content
            .windowHolder(windowHolder)
            .onChange(of: presentedData) { presentedData in
                if let presentedData {
                    showAlert(contentBuilder(presentedData))
                }
            }
            // effectively onAppear
            .onChange(of: windowHolder.window) { window in
                guard window != nil else { return }
                if let presentedData {
                    showAlert(contentBuilder(presentedData))
                }
            }
    }

    private func showAlert(_ content: AKAlertBody) {
        guard let window = windowHolder.window else { return }
        if presented {
            return
        }

        let alert = NSAlert()
        if let title = content.title {
            alert.messageText = title
        }
        if let desc = content.desc {
            // always use scrollable if text is too long
            if content.scrollable || desc.count > MAX_INLINE_DESC_CHARS {
                let scrollView = NSTextView.scrollableTextView()
                scrollView.frame = NSRect(x: 0, y: 0, width: 650, height: 400)
                let textView = scrollView.documentView as! NSTextView

                textView.textContainerInset = NSSize(width: 0, height: 0)
                textView.isEditable = false
                //textView.drawsBackground = false
                textView.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
                textView.string = desc

                alert.accessoryView = scrollView

                NSAnimationContext.beginGrouping()
                NSAnimationContext.current.duration = 0
                textView.scrollToEndOfDocument(nil)
                NSAnimationContext.endGrouping()
            } else {
                alert.informativeText = desc
            }
        }
        alert.alertStyle = style
        for spec in content.buttons {
            let nsButton = alert.addButton(withTitle: spec.title)
            nsButton.hasDestructiveAction = spec.destructive
        }
        if content.buttons.isEmpty {
            alert.addButton(withTitle: "OK")
        }
        presented = true
        alert.beginSheetModal(for: window) { response in
            if response == .alertFirstButtonReturn,
                content.buttons.count >= 1
            {
                content.buttons[0].action()
            } else if response == .alertSecondButtonReturn,
                content.buttons.count >= 2
            {
                content.buttons[1].action()
            } else if response == .alertThirdButtonReturn,
                content.buttons.count >= 3
            {
                content.buttons[2].action()
            }

            presentedData = nil
            presented = false
        }
    }
}

enum AKAlertFlags {
    case scrollable
}

struct AKAlertButton {
    var title: String
    var destructive: Bool = false
    var action: () -> Void

    init(
        _ title: String,
        destructive: Bool = false,
        action: @escaping () -> Void = {}
    ) {
        self.title = title
        self.destructive = destructive
        self.action = action
    }
}

struct AKAlertBody {
    var title: String?
    var desc: String?
    var scrollable: Bool
    var buttons: [AKAlertButton]
}

extension View {
    func akAlert<T: Equatable>(
        presentedValue: Binding<T?>,
        style: NSAlert.Style = .warning,
        @AKAlertBuilder content: @escaping (T) -> AKAlertBody
    ) -> some View {
        modifier(AKAlert(presentedData: presentedValue, style: style, contentBuilder: content))
    }

    func akAlert(
        isPresented: Binding<Bool>,
        style: NSAlert.Style = .warning,
        @AKAlertBuilder content: @escaping () -> AKAlertBody
    ) -> some View {
        let binding = Binding<Bool?>(
            get: { isPresented.wrappedValue ? true : nil },
            set: { isPresented.wrappedValue = $0 != nil })
        return self.akAlert(presentedValue: binding, style: style) { _ in
            content()
        }
    }
}

@resultBuilder
struct AKAlertBuilder {
    static func buildExpression(_ first: AKAlertFlags) -> AKAlertBody {
        AKAlertBody(title: nil, desc: nil, scrollable: first == .scrollable, buttons: [])
    }

    static func buildExpression(_ first: AKAlertButton) -> AKAlertBody {
        AKAlertBody(title: nil, desc: nil, scrollable: false, buttons: [first])
    }

    // default case
    static func buildExpression<T>(_ first: T) -> T {
        first
    }

    static func buildPartialBlock(first: String) -> AKAlertBody {
        AKAlertBody(title: first, desc: nil, scrollable: false, buttons: [])
    }

    static func buildPartialBlock(first: AKAlertBody) -> AKAlertBody {
        first
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: String?) -> AKAlertBody {
        AKAlertBody(
            title: accumulated.title, desc: next, scrollable: accumulated.scrollable,
            buttons: accumulated.buttons)
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: AKAlertBody) -> AKAlertBody {
        AKAlertBody(
            title: next.title ?? accumulated.title, desc: next.desc ?? accumulated.desc,
            scrollable: next.scrollable || accumulated.scrollable,
            buttons: accumulated.buttons + next.buttons)
    }

    static func buildEither<T>(first: T) -> T {
        first
    }

    static func buildEither<T>(second: T) -> T {
        second
    }

    static func buildOptional(_ component: AKAlertBody?) -> AKAlertBody {
        component ?? AKAlertBody(title: nil, desc: nil, scrollable: false, buttons: [])
    }
}
