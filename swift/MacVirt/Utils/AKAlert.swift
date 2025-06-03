//
// Created by Danny Lin on 3/7/24.
//

import AppKit
import Foundation
import SwiftUI

private let MAX_INLINE_DESC_CHARS = 500

struct AKAlert<T: Equatable>: ViewModifier {
    @Binding var presentedData: T?
    @StateObject private var windowHolder = WindowHolder()
    // prevent duplicate queued modal from onChange(of: presentedData) and onChange(of: window)
    @State private var presented = false
    let contentBuilder: (T) -> AKAlertContent

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

    private func showAlert(_ content: AKAlertContent) {
        guard let window = windowHolder.window else { return }
        if presented {
            return
        }

        let alert = NSAlert()
        alert.messageText = content.title
        if let desc = content.desc {
            if content.scrollable {
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
        alert.alertStyle = content.style
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
               content.buttons.count >= 1 {
                content.buttons[0].action()
            } else if response == .alertSecondButtonReturn,
               content.buttons.count >= 2 {
                content.buttons[1].action()
            } else if response == .alertThirdButtonReturn,
               content.buttons.count >= 3 {
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

struct AKAlertContent {
    var title: String
    var desc: String?
    var scrollable: Bool
    var style: NSAlert.Style = .warning
    var buttons: [AKAlertButton] = []

    init(
        _ title: String,
        desc: String? = nil,
        scrollable: Bool = false,
        style: NSAlert.Style = .warning,
        buttons: [AKAlertButton] = []
    ) {
        self.title = title
        self.desc = desc
        // always use scrollable if text is too long
        self.scrollable = scrollable || (desc?.count ?? 0) > MAX_INLINE_DESC_CHARS
        self.style = style
        self.buttons = buttons
    }
}

struct AKAlertBody {
    var title: String?
    var desc: String?
    var scrollable: Bool
    var buttons: [AKAlertButton]
}

extension View {
    private func akAlert<T: Equatable>(
        presentedValue: Binding<T?>,
        contentBuilder: @escaping (T) -> AKAlertContent
    ) -> some View {
        self.modifier(AKAlert(presentedData: presentedValue, contentBuilder: contentBuilder))
    }

    func akAlert<T: Equatable>(
        presentedValue: Binding<T?>,
        style: NSAlert.Style = .warning,
        scrollable: Bool = false,
        @AKAlertBuilder content: @escaping (T) -> AKAlertBody
    ) -> some View {
        return self.akAlert(presentedValue: presentedValue) { value in
            let body = content(value)
            return AKAlertContent(
                body.title ?? "",
                desc: body.desc,
                scrollable: scrollable,
                buttons: body.buttons)
        }
    }

    func akAlert(
        isPresented: Binding<Bool>,
        style: NSAlert.Style = .warning,
        scrollable: Bool = false,
        @AKAlertBuilder content: @escaping () -> AKAlertBody
    ) -> some View {
        let binding = Binding<Bool?>(
            get: { isPresented.wrappedValue ? true : nil },
            set: { isPresented.wrappedValue = $0 != nil })
        return self.akAlert(presentedValue: binding) { _ in
            let body = content()
            return AKAlertContent(
                body.title ?? "",
                desc: body.desc,
                scrollable: scrollable,
                buttons: body.buttons)
        }
    }
}

@resultBuilder
struct AKAlertBuilder {
    static func buildPartialBlock(first: String) -> AKAlertBody {
        AKAlertBody(title: first, desc: nil, scrollable: false, buttons: [])
    }

    static func buildPartialBlock(first: AKAlertFlags) -> AKAlertBody {
        AKAlertBody(title: nil, desc: nil, scrollable: first == .scrollable, buttons: [])
    }

    static func buildPartialBlock(first: AKAlertButton) -> AKAlertBody {
        AKAlertBody(title: nil, desc: nil, scrollable: false, buttons: [first])
    }

    static func buildPartialBlock(first: AKAlertBody) -> AKAlertBody {
        first
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: AKAlertBody) -> AKAlertBody {
        AKAlertBody(title: next.title ?? accumulated.title, desc: next.desc ?? accumulated.desc, scrollable: next.scrollable || accumulated.scrollable, buttons: accumulated.buttons + next.buttons)
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: AKAlertButton) -> AKAlertBody {
        AKAlertBody(title: accumulated.title, desc: accumulated.desc, scrollable: accumulated.scrollable, buttons: accumulated.buttons + [next])
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: String?) -> AKAlertBody {
        AKAlertBody(title: accumulated.title, desc: next, scrollable: accumulated.scrollable, buttons: accumulated.buttons)
    }

    static func buildPartialBlock(accumulated: AKAlertBody, next: AKAlertFlags) -> AKAlertBody {
        AKAlertBody(title: accumulated.title, desc: accumulated.desc, scrollable: next == .scrollable, buttons: accumulated.buttons)
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
