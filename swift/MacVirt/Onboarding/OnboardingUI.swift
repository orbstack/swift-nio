//
// Created by Danny Lin on 5/22/23.
//

import Foundation
import SwiftUI

private struct CtaButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.vertical, 8)
            .padding(.horizontal, 16)
            .background(configuration.isPressed ? Color.accentColor : Color.accentColor)
            .foregroundColor(.primary)
            .cornerRadius(6.0)
    }
}

struct CtaButton: View {
    private static let radius = 8.0

    let label: String
    let action: () -> Void

    @Environment(\.colorScheme) private var colorScheme: ColorScheme
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @State private var hoverOpacity = 0.0
    @State private var activeOpacity = 0.0

    init(label: String, action: @escaping () -> Void) {
        self.label = label
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            VStack {
                Text(label)
                    .font(.title3)
                    .fontWeight(.medium)
                    .foregroundColor(colorScheme == .light && controlActiveState == .key ? .white : .primary)
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 16)
            .background(Color(NSColor.controlAccentColor), in: RoundedRectangle(cornerRadius: Self.radius))
            .cornerRadius(Self.radius)
            .overlay(
                RoundedRectangle(cornerRadius: Self.radius)
                    .stroke(Color.primary.opacity(0.1 + 0.15 * hoverOpacity), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .keyboardShortcut(.defaultAction)
        .onHover {
            if $0 {
                withAnimation(.spring().speed(2)) {
                    hoverOpacity = 1
                }
            } else {
                withAnimation(.spring().speed(2)) {
                    hoverOpacity = 0
                }
            }
        }
    }
}
