import SwiftUI
import Combine

struct StatusOverlayBadge<S: SetAlgebra>: View {
    @State private var opacity = 0.0

    let title: String
    let progressSet: S
    let progressPublisher: Published<S>.Publisher

    init(_ title: String, set progressSet: S, publisher progressPublisher: Published<S>.Publisher) {
        self.title = title
        self.progressSet = progressSet
        self.progressPublisher = progressPublisher
    }

    var body: some View {
        HStack {
            Text(title)
            ProgressView()
                .scaleEffect(0.5)
                .frame(width: 16, height: 16)
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 12)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8)
            .stroke(.gray.opacity(0.25), lineWidth: 0.5))
        .opacity(opacity)
        .padding(16)
        .onAppear {
            opacity = progressSet.isEmpty ? 0 : 1
        }
        .onReceive(progressPublisher) { value in
            withAnimation {
                opacity = value.isEmpty ? 0 : 1
            }
        }
    }
}
