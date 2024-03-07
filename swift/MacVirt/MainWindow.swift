//
//  MainWindow.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import CachedAsyncImage
import Defaults
import Sparkle
import SwiftUI
import UserNotifications

private let avatarRadius: Float = 32
private let statusDotRadius: Float = 8
private let statusMarginRadius: Float = 12

struct NavTab: View {
    private let label: String
    private let systemImage: String

    init(_ label: String, systemImage: String) {
        self.label = label
        self.systemImage = systemImage
    }

    var body: some View {
        Label(label, systemImage: systemImage)
            .padding(.vertical, 4)
    }
}

struct UserSwitcherButton: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @Binding var presentAuth: Bool

    var body: some View {
        let isLoggedIn = vmModel.drmState.isSignedIn
        Button {
            if isLoggedIn {
                // manage account
                NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
            } else {
                presentAuth = true
            }
        } label: {
            HStack(spacing: 0) {
                var drmState = vmModel.drmState

                let subtitle = drmState.subtitle
                // "Personal use only" is shown in badge instead
                let showStatusDotAndSubtitle = subtitle != "Personal use only"

                Group {
                    if let imageURL = drmState.imageURL {
                        CachedAsyncImage(url: imageURL) { image in
                            image
                                .resizable()
                                // better interp to fix pixelation
                                .interpolation(.high)
                                // clip to circle
                                .clipShape(Circle())
                        } placeholder: {
                            Image(systemName: "person.circle")
                                .resizable()
                                .foregroundColor(.accentColor)
                        }
                    } else {
                        Image(systemName: "person.circle")
                            .resizable()
                            .foregroundColor(.accentColor)
                    }
                }
                .frame(width: CGFloat(avatarRadius), height: CGFloat(avatarRadius))
                // mask
                .mask {
                    Rectangle()
                        .overlay(alignment: .topLeading) {
                            if showStatusDotAndSubtitle {
                                // calculate a position intersecting the circle and y=-x from the bottom-right edge
                                let x = avatarRadius * cos(Float.pi / 4) + (statusDotRadius / 2)
                                let y = avatarRadius * sin(Float.pi / 4) + (statusDotRadius / 2)

                                Circle()
                                .frame(width: CGFloat(statusMarginRadius), height: CGFloat(statusMarginRadius))
                                .position(x: CGFloat(x), y: CGFloat(y))
                                .blendMode(.destinationOut)
                            }
                        }
                }
                // status dot
                .overlay(alignment: .topLeading) {
                    if showStatusDotAndSubtitle {
                        // calculate a position intersecting the circle and y=-x from the bottom-right edge
                        let x = avatarRadius * cos(Float.pi / 4) + (statusDotRadius / 2)
                        let y = avatarRadius * sin(Float.pi / 4) + (statusDotRadius / 2)

                        Circle()
                        .fill(drmState.statusDotColor.opacity(0.85))
                        .frame(width: CGFloat(statusDotRadius), height: CGFloat(statusDotRadius))
                        .position(x: CGFloat(x), y: CGFloat(y))
                    }
                }
                .padding(.trailing, 8)

                VStack(alignment: .leading) {
                    Text(drmState.title)
                        .font(.headline)
                        .lineLimit(1)

                    // shown in badge
                    if showStatusDotAndSubtitle {
                        Text(drmState.subtitle)
                        .font(.subheadline)
                    }
                }

                // occupy all right space for border
                Spacer()
            }
            .padding(12)
            .onRawDoubleClick {}
        }
        .buttonStyle(.plain)
        // occupy full rect
        .contextMenu {
            Button("Manage…") {
                NSWorkspace.shared.open(URL(string: "https://orbstack.dev/dashboard")!)
            }
            .disabled(!isLoggedIn)

            Button("Switch Organization…") {
                // simple: just reauth and use web org picker
                presentAuth = true
            }
            .disabled(!isLoggedIn)

            Divider()

            Button("Refresh") {
                Task { @MainActor in
                    await vmModel.tryRefreshDrm()
                }
            }
            .disabled(!isLoggedIn)

            Button("Sign Out") {
                Task { @MainActor in
                    await vmModel.trySignOut()
                }
            }
            .disabled(!isLoggedIn)
        }
        .border(width: 1, edges: [.top], color: Color(NSColor.separatorColor).opacity(0.5))
    }
}

extension View {
    func toolbarMacOS13<Content: CustomizableToolbarContent>(id: String, @ToolbarContentBuilder content: () -> Content) -> some View {
        if #available(macOS 13.0, *) {
            return self.toolbar(id: id, content: content)
        } else {
            return self
        }
    }
}

func truncateError(description: String) -> String {
    if description.count > 2500 {
        return String(description.prefix(1250)) + "…" + String(description.suffix(1250))
    } else {
        return description
    }
}
