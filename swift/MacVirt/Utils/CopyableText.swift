//
// Created by Danny Lin on 9/5/23.
//

import Foundation
import SwiftUI

struct CopyableText: View {
    private let text: String
    private let copyAs: String

    @State private var isCopied = false
    @State private var hoverOpacity = 0.0

    init(_ text: String, copyAs: String? = nil) {
        self.text = text
        self.copyAs = copyAs ?? text
    }

    var body: some View {
        Button(action: copy) {
            HStack {
                Text(text)

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

struct CopyableText_Previews: PreviewProvider {
    static var previews: some View {
        CopyableText("Hello, world!")
    }
}
