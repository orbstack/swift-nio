import SwiftUI

private let maxToasts = 4
private let defaultDuration: TimeInterval = 5

enum ToastType {
    case success
    case info
    case warning
    case error
}

struct Toast: Identifiable {
    let id = UUID()
    let type: ToastType
    let title: AttributedString
    let message: AttributedString
    let duration: TimeInterval
    let action: (() -> Void)?
}

class Toaster: ObservableObject {
    @Published var toasts: [Toast] = []

    func add(toast: Toast) {
        if toasts.contains(where: { $0.id == toast.id }) {
            return
        }

        withAnimation {
            if toasts.count >= maxToasts {
                toasts.removeFirst()
            }

            toasts.append(toast)
            self.toasts = toasts
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + toast.duration) {
            withAnimation { 
                self.toasts.removeAll { $0.id == toast.id }
            }
        }
    }

    func error(title: String, message: String, duration: TimeInterval = defaultDuration, action: (() -> Void)? = nil) {
        add(toast: Toast(type: .error, title: AttributedString(title), message: AttributedString(message), duration: duration, action: action))
    }

    func error(title: String, error: any Error, duration: TimeInterval = defaultDuration, action: (() -> Void)? = nil) {
        NSLog("toasting error: [\(title)] \(error)")
        // localizedDescription i
        add(toast: Toast(type: .error, title: AttributedString(title), message: AttributedString("\(error)"), duration: duration, action: action))
    }
}
