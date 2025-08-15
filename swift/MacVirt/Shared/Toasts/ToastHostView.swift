import SwiftUI

struct ToastHostView: View {
    @EnvironmentObject var toaster: Toaster

    var body: some View {
        VStack {
            ForEach(toaster.toasts) { toast in
                ToastView(toast: toast)
                .tag(toast.id)
            }
        }
        .padding()
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

    @State private var closeButtonHovered = false

    let toast: Toast

    var body: some View {
        VStack(alignment: .leading) {
            HStack(alignment: .center, spacing: 8) {
                Group {
                    switch toast.type {
                    case .success:
                        Image(systemName: "checkmark.circle.fill")
                        .resizable()
                        .foregroundStyle(.green)
                    case .info:
                        Image(systemName: "info.circle.fill")
                        .resizable()
                    case .warning:
                        Image(systemName: "exclamationmark.triangle.fill")
                        .resizable()
                        .foregroundStyle(.yellow)
                    case .error:
                        Image(systemName: "xmark.circle.fill")
                        .resizable()
                        .foregroundStyle(.red)
                    }
                }
                .frame(width: 16, height: 16)

                Text(toast.title)
                .foregroundStyle(.primary)
            }

            HStack(alignment: .top, spacing: 8) {
                Spacer()
                .frame(width: 16, height: 16)

                Text(toast.message)
                .foregroundStyle(.secondary)
            }
        }
        .padding()
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16)
            .stroke(.secondary.opacity(0.25), lineWidth: 1))
        .frame(width: 375)
        .compositingGroup()
        .shadow(radius: 2)
        .overlay(alignment: .topLeading) {
            Button {
                withAnimation {
                    toaster.toasts.removeAll { $0.id == toast.id }
                }
            } label: {
                Image(systemName: "xmark")
                    .resizable()
                .frame(width: 8, height: 8)
            }
            .buttonStyle(.plain)
            .padding(4)
            .background(.regularMaterial, in: .circle)
            .shadow(radius: 2)
            .opacity(closeButtonHovered ? 1 : 0)
        }
        .onHover { hovered in
            withAnimation {
                closeButtonHovered = hovered
            }
        }
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}
