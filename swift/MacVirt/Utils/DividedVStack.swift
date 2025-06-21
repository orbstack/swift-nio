//
//  DividedVStack.swift
//  MacVirt
//
//  Created by Andrew Zheng (github.com/aheze) on 1/1/24.
//  Copyright © 2024 Andrew Zheng. All rights reserved.
//

import SwiftUI

private struct NavigationLinkLabelStyle: LabelStyle {
    @Environment(\.colorScheme) private var colorScheme
    @ScaledMetric(relativeTo: .body) var iconSize = 24

    func makeBody(configuration: Configuration) -> some View {
        HStack {
            configuration.icon
                // set a frame so it lines up
                .frame(width: iconSize, height: iconSize, alignment: .center)
                .foregroundStyle(
                    colorScheme == .light ? Color(NSColor.windowBackgroundColor) : Color.primary
                )
                .background(
                    colorScheme == .light
                        ? Color.primary.opacity(0.75) : Color.secondary.opacity(0.3),
                    in: RoundedRectangle(cornerSize: CGSize(width: 8, height: 8)))

            configuration.title
                .frame(maxWidth: .infinity, alignment: .leading)

            Spacer()

            Image(systemName: "chevron.right")
                .imageScale(.small)
                .foregroundStyle(.tertiary)
                .fontWeight(.semibold)
        }
        .padding(10)
    }
}

private struct NavigationLinkButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .background(
                configuration.isPressed
                    // close enough
                    ? Color.secondary.opacity(0.05)
                    : Color.clear
            )
    }
}

struct DividedRowButton<Label: View>: View {
    @Environment(\.isEnabled) private var isEnabled

    private let action: () -> Void
    @ViewBuilder private let label: () -> Label

    init(action: @escaping () -> Void, @ViewBuilder label: @escaping () -> Label) {
        self.action = action
        self.label = label
    }

    var body: some View {
        Button(
            action: action,
            label: {
                label()
                    .labelStyle(NavigationLinkLabelStyle())
            }
        )
        .buttonStyle(NavigationLinkButtonStyle())
        .padding(-10)
        .opacity(isEnabled ? 1 : 0.75)
    }
}

struct DividedButtonStack<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        Form {
            content()
        }
        .formStyle(.grouped)
        // ??? ignoresSafeArea doesn't remove the builtin padding
        .padding(-10)
    }
}
