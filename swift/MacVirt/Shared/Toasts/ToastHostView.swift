import SwiftUI

struct ToastHostView: View {
    @EnvironmentObject var toaster: Toaster

    var body: some View {
        VStack(alignment: .trailing) {
            ForEach(toaster.toasts) { toast in
                ToastView(toast: toast)
                .tag(toast.id)
            }
        }
    }
}

private struct ToastHostViewModifier: ViewModifier {
    @StateObject private var toaster = Toaster()

    func body(content: Content) -> some View {
        content
        .overlay(alignment: .bottomTrailing) {
            ToastHostView()
        }
        .environmentObject(toaster)
    }
}

extension View {
    func toastOverlay() -> some View {
        self.modifier(ToastHostViewModifier())
    }
}

private struct ToastView: View {
    @EnvironmentObject var toaster: Toaster

    let toast: Toast

    var body: some View {
        HStack(alignment: .top) {
            switch toast.type {
            case .success:
                Image(systemName: "checkmark.circle")
                .foregroundStyle(.green)
            case .info:
                Image(systemName: "info.circle")
            case .warning:
                Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(.yellow)
            case .error:
                Image(systemName: "xmark.circle")
                .foregroundStyle(.red)
            }

            VStack(alignment: .leading) {
                Text(toast.title)
                .foregroundStyle(.primary)
                Text(toast.message)
                .foregroundStyle(.secondary)
            }

            Button {
                toaster.toasts.removeAll { $0.id == toast.id }
            } label: {
                Image(systemName: "xmark")
            }
            .buttonStyle(.plain)
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .shadow(radius: 10)
        .padding()
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}
