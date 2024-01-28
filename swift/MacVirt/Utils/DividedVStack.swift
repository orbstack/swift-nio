//
//  DividedVStack.swift
//  MacVirt
//
//  Created by Andrew Zheng (github.com/aheze) on 1/1/24.
//  Copyright © 2024 Andrew Zheng. All rights reserved.
//

import SwiftUI

/// A vertical stack that adds separators
/// From https://movingparts.io/variadic-views-in-swiftui
struct DividedVStack<Content: View>: View {
    var leadingMargin: CGFloat
    var trailingMargin: CGFloat
    var color: Color?
    var content: Content

    public init(
        leadingMargin: CGFloat = 0,
        trailingMargin: CGFloat = 0,
        color: Color? = nil,
        @ViewBuilder content: () -> Content
    ) {
        self.leadingMargin = leadingMargin
        self.trailingMargin = trailingMargin
        self.color = color
        self.content = content()
    }

    public var body: some View {
        _VariadicView.Tree(
            DividedVStackLayout(
                leadingMargin: leadingMargin,
                trailingMargin: trailingMargin,
                color: color
            )
        ) {
            content
        }
    }
}

struct DividedVStackLayout: _VariadicView_UnaryViewRoot {
    var leadingMargin: CGFloat
    var trailingMargin: CGFloat
    var color: Color?

    @ViewBuilder
    public func body(children: _VariadicView.Children) -> some View {
        let last = children.last?.id

        VStack(spacing: 0) {
            ForEach(children) { child in
                child

                if child.id != last {
                    Divider()
                        .opacity(color == nil ? 1 : 0)
                        .overlay(
                            color ?? .clear
                        )
                        .padding(.leading, leadingMargin)
                        .padding(.trailing, trailingMargin)
                }
            }
        }
    }
}

struct DividedRowButton<Label: View>: View {
    private let action: () -> Void
    @ViewBuilder private let label: () -> Label

    init(action: @escaping () -> Void, @ViewBuilder label: @escaping () -> Label) {
        self.action = action
        self.label = label
    }

    var body: some View {
        Button(action: action) {
            label()
                .labelStyle(ItemRowLabelStyle())
        }
        .buttonStyle(ItemRowButtonStyle())
    }
}

struct DividedButtonStack<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        DividedVStack {
            content()
        }
        .background {
            RoundedRectangle(cornerRadius: 8)
                .fill(.ultraThinMaterial)
                .overlay {
                    RoundedRectangle(cornerRadius: 8)
                        .strokeBorder(Color.secondary, lineWidth: 1)
                        .opacity(0.1)
                }
        }
    }
}
