//
// Created by Danny Lin on 9/5/23.
//

import Foundation
import SwiftUI

enum CopyButtonSide {
    case left
    case right
}

struct CopyButtonSideKey: EnvironmentKey {
    static let defaultValue: CopyButtonSide = .right
}

extension EnvironmentValues {
    var copyButtonSide: CopyButtonSide {
        get { self[CopyButtonSideKey.self] }
        set { self[CopyButtonSideKey.self] = newValue }
    }
}

extension View {
    func copyButtonSide(_ side: CopyButtonSide) -> some View {
        environment(\.copyButtonSide, side)
    }
}

struct CopyableText<Content: View>: View {
    @ViewBuilder private let textBuilder: () -> Content
    @Environment(\.copyButtonSide) private var copyButtonSide
    private let copyAs: String

    @State private var isCopied = false
    @State private var hoverOpacity = 0.0

    init(copyAs: String, @ViewBuilder text: @escaping () -> Content) {
        self.copyAs = copyAs
        textBuilder = text
    }

    private var copyButton: some View {
        Image(systemName: "doc.on.doc")
            .resizable()
            .fontWeight(.bold)
            .aspectRatio(contentMode: .fit)
            .foregroundColor(.secondary)
            .frame(width: 12, height: NSFont.systemFontSize)
            .padding(2)
            .background(.secondary.opacity(0.1), in: RoundedRectangle(cornerRadius: 4))
            .opacity(hoverOpacity)
            .help("Copy")
    }

    private var successButton: some View {
        Image(systemName: "checkmark")
            .resizable()
            .fontWeight(.bold)
            .aspectRatio(contentMode: .fit)
            .foregroundColor(.green)
            .frame(width: 10, height: NSFont.systemFontSize)
            .padding(.vertical, 2)
            .padding(.horizontal, 3)
            .background(.secondary.opacity(0.1), in: RoundedRectangle(cornerRadius: 4))
            .opacity(hoverOpacity)
            .help("Copied")
    }

    var body: some View {
        Button(action: copy) {
            // default 8
            HStack(spacing: 6) {
                if copyButtonSide == .left {
                    if isCopied {
                        successButton
                    } else {
                        copyButton
                    }
                }

                textBuilder()

                if copyButtonSide == .right {
                    if isCopied {
                        successButton
                    } else {
                        copyButton
                    }
                }
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
