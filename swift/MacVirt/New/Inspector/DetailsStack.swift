//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DetailsStack<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        HStack {
            // keep this at leading
            VStack(alignment: .leading, spacing: 20) {
                content()
            }

            Spacer()
        }
        // ideally only horizontal on macOS 14, but on macOS 12 toolbar is opaque,
        // so no vertical padding is ugly
        .padding(20)
    }
}

struct DetailsSection<Content: View>: View {
    private let label: String
    private let indent: CGFloat
    @ViewBuilder private let content: () -> Content

    init(_ label: String, indent: CGFloat = 16, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.indent = indent
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(.headline)

            VStack(alignment: .leading, spacing: 24) {
                content()
            }
            .padding(.leading, indent)
        }
    }
}

struct ScrollableDetailsSection<Content: View>: View {
    private let label: String
    private let indent: CGFloat
    @ViewBuilder private let content: () -> Content

    init(_ label: String, indent: CGFloat = 16, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.indent = indent
        self.content = content
    }

    var body: some View {
        DetailsSection(label, indent: 0) {
            ScrollView(.horizontal) {
                content()
                    .padding(4)
                    .padding(.leading, indent)
            }
            .background {
                Rectangle()
                    .fill(.ultraThinMaterial)
                    .overlay {
                        Rectangle()
                            .strokeBorder(Color.secondary, lineWidth: 1)
                            .opacity(0.1)
                    }
            }
        }
    }
}
