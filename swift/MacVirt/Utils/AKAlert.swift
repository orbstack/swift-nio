//
// Created by Danny Lin on 3/7/24.
//

import Foundation
import SwiftUI
import AppKit

private let MAX_INLINE_DESC_CHARS = 500

struct AKAlert<T: Equatable>: ViewModifier {
    @Binding var presentedData: T?
    @StateObject private var windowHolder = WindowHolder()
    // prevent duplicate queued modal from onChange(of: presentedData) and onChange(of: window)
    @State private var presented = false
    let contentBuilder: (T) -> AKAlertContent

    func body(content: Content) -> some View {
        content
            .background(WindowAccessor(holder: windowHolder))
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
            if content.scrollableText {
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
        if let button1Label = content.button1Label {
            alert.addButton(withTitle: button1Label)
        } else {
            // if there are no buttons, add "OK"
            alert.addButton(withTitle: "OK")
        }
        if let button2Label = content.button2Label {
            alert.addButton(withTitle: button2Label)
        }
        presented = true
        alert.beginSheetModal(for: window) { response in
            if response == .alertFirstButtonReturn {
                if let button1Action = content.button1Action {
                    button1Action()
                }
            } else if response == .alertSecondButtonReturn {
                if let button2Action = content.button2Action {
                    button2Action()
                }
            }
            presentedData = nil
            presented = false
        }
    }
}

struct AKAlertContent {
    var title: String
    var desc: String?
    var scrollableText: Bool
    var style: NSAlert.Style = .warning
    var button1Label: String? = nil
    var button1Action: (() -> Void)? = nil
    var button2Label: String? = nil
    var button2Action: (() -> Void)? = nil

    init(title: String,
         desc: String? = nil,
         scrollableText: Bool = false,
         style: NSAlert.Style = .warning,
         button1Label: String? = nil,
         button1Action: (() -> Void)? = nil,
         button2Label: String? = nil,
         button2Action: (() -> Void)? = nil) {
        self.title = title
        self.desc = desc
        // always use scrollable if text is too long
        self.scrollableText = scrollableText || (desc?.count ?? 0) > MAX_INLINE_DESC_CHARS
        self.style = style
        self.button1Label = button1Label
        self.button1Action = button1Action
        self.button2Label = button2Label
        self.button2Action = button2Action
    }

    mutating func addButton(_ title: String, _ action: @escaping () -> Void = { }) {
        if button1Label == nil {
            button1Label = title
        } else if button2Label == nil {
            button2Label = title
        }
        if button1Action == nil {
            button1Action = action
        } else if button2Action == nil {
            button2Action = action
        }
    }
}

extension View {
    func akAlert<T: Equatable>(presentedValue: Binding<T?>,
                 contentBuilder: @escaping (T) -> AKAlertContent) -> some View {
        self.modifier(AKAlert(presentedData: presentedValue, contentBuilder: contentBuilder))
    }

    func akAlert(_ title: String,
                 isPresented: Binding<Bool>,
                 desc: (() -> String)? = nil,
                 scrollableText: Bool = false,
                 button1Label: String? = nil,
                 button1Action: (() -> Void)? = nil,
                 button2Label: String? = nil,
                 button2Action: (() -> Void)? = nil) -> some View {
        let binding = Binding<Bool?>(get: { isPresented.wrappedValue ? true : nil },
                                      set: { isPresented.wrappedValue = $0 != nil })
        return self.akAlert(presentedValue: binding) { _ in
            AKAlertContent(title: title,
                           desc: desc?(),
                           scrollableText: scrollableText,
                           button1Label: button1Label,
                           button1Action: button1Action,
                           button2Label: button2Label,
                           button2Action: button2Action)
        }
    }

    func akAlert(_ title: String,
                 isPresented: Binding<Bool>,
                 desc: String? = nil,
                 scrollableText: Bool = false,
                 button1Label: String? = nil,
                 button1Action: (() -> Void)? = nil,
                 button2Label: String? = nil,
                 button2Action: (() -> Void)? = nil) -> some View {
        self.akAlert(title,
                     isPresented: isPresented,
                     desc: desc == nil ? nil : { desc! },
                     scrollableText: scrollableText,
                     button1Label: button1Label,
                     button1Action: button1Action,
                     button2Label: button2Label,
                     button2Action: button2Action)
    }
}
