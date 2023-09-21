//
// Created by Danny Lin on 9/5/23.
//

import Foundation
import SwiftUI

struct CopyableText<Content: View>: View {
    @ViewBuilder private let textBuilder: () -> Content
    private let copyAs: String

    @State private var isCopied = false
    @State private var hoverOpacity = 0.0

    init(copyAs: String, @ViewBuilder text: @escaping () -> Content) {
        self.copyAs = copyAs
        self.textBuilder = text
    }

    var body: some View {
        Button(action: copy) {
            HStack {
                textBuilder()

                Image(systemName: isCopied ? "checkmark.circle.fill" : "doc.on.doc")
                .resizable()
                .aspectRatio(contentMode: .fit)
                .foregroundColor(.secondary)
                // avoid checkmark and doc.on.doc having diff widths
                // (maxWidth: 12, maxHeight: .infinity) steals height from other Texts in CommandsRootView
                .frame(width: 12, height: NSFont.systemFontSize)
                .opacity(hoverOpacity)
                .help("Copy")
            }
        }
        .buttonStyle(.plain)
        .onHover { hovered in
            if hovered {
                // reset before next hover to avoid flash on unhover
                isCopied = false
            }

            withAnimation(.spring().speed(2)) {
                hoverOpacity = hovered ? 1 : 0
            }
        }
        .onTapGesture(perform: copy)
        .accessibilityLabel("Copy \(copyAs)")
    }

    private func copy() {
        NSPasteboard.copy(copyAs)
        isCopied = true
    }
}

extension CopyableText where Content == Text {
    init(_ text: String, copyAs: String? = nil) {
        self.init(copyAs: copyAs ?? text) {
            Text(text)
        }
    }
}

struct CopyableText_Previews: PreviewProvider {
    static var previews: some View {
        CopyableText("Hello, world!")
    }
}

struct AuxiliaryCopyableText<Content: View>: View {
    @ViewBuilder private let textBuilder: () -> Content
    private let copyAs: String

    @State private var isCopied = false
    @State private var hoverOpacity = 0.0

    init(copyAs: String, @ViewBuilder text: @escaping () -> Content) {
        self.copyAs = copyAs
        self.textBuilder = text
    }

    var body: some View {
        HStack {
            textBuilder()

            Button(action: copy) {
                Image(systemName: isCopied ? "checkmark.circle.fill" : "doc.on.doc")
                .resizable()
                .aspectRatio(contentMode: .fit)
                .foregroundColor(.secondary)
                // avoid checkmark and doc.on.doc having diff widths
                // (maxWidth: 12, maxHeight: .infinity) steals height from other Texts in CommandsRootView
                .frame(width: 12, height: NSFont.systemFontSize)
                .opacity(hoverOpacity)
                .help("Copy")
            }
            .buttonStyle(.plain)
            .onTapGesture(perform: copy)
            .accessibilityLabel("Copy \(copyAs)")
        }
        .onHover { hovered in
            if hovered {
                // reset before next hover to avoid flash on unhover
                isCopied = false
            }

            withAnimation(.spring().speed(2)) {
                hoverOpacity = hovered ? 1 : 0
            }
        }
    }

    private func copy() {
        NSPasteboard.copy(copyAs)
        isCopied = true
    }
}
