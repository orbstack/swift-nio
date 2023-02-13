//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

fileprivate struct CtaButtonStyle: ButtonStyle {
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
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: Self.radius))
            .background(Color.accentColor.opacity(0.9 + hoverOpacity * 0.1), in: RoundedRectangle(cornerRadius: Self.radius))
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

fileprivate struct WelcomePoint: View {
    let systemImage: String
    let color: Color
    let title: String
    let desc: String

    var body: some View {
        HStack {
            HStack {
                Image(systemName: systemImage)
                    .resizable()
                    .frame(width: 32, height: 32)
                    .foregroundColor(color)
                Text(title)
                    .font(.headline)
                Spacer()
            }.frame(width: 100)
            Text(desc)
                    .font(.body)
                    .foregroundColor(.secondary)
            Spacer()
        }
        .padding(.horizontal)
        .frame(maxWidth: .infinity)
    }
}

struct OnboardingWelcomeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    let onboardingController: OnboardingController

    var body: some View {
        VStack {
            Text(Constants.userAppName)
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("Fast, light, simple Linux machines and containers")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            VStack(alignment: .leading, spacing: 16) {
                WelcomePoint(
                    systemImage: "bolt.fill",
                    color: .orange,
                    title: "Fast.",
                    desc: "Starts fast, optimized networking and disk, fast x86 with Rosetta"
                )
                if #available(macOS 13.0, *) {
                    WelcomePoint(
                        systemImage: "wind.circle.fill",
                        color: .blue,
                        title: "Light.",
                        desc: "Low CPU and disk usage, works with less memory, native app"
                    )
                } else {
                    WelcomePoint(
                        systemImage: "wind",
                        color: .blue,
                        title: "Light.",
                        desc: "Low CPU and disk usage, works with less memory, native app"
                    )
                }
                WelcomePoint(
                    systemImage: "checkmark.circle.fill",
                    color: .green,
                    title: "Simple.",
                    desc: "Minimal setup, seamless Docker, 2-way CLI integration, file access from macOS and Linux, works with VPNs"
                )
            }.padding(.horizontal)

            Spacer()

            Text("By continuing, you agree to our [Terms](https://orbstack.dev/terms) and [Privacy Policy](https://orbstack.dev/privacy-policy).")
                .font(.subheadline)
                .foregroundColor(.secondary)
                .padding(.bottom, 16)

            HStack(alignment: .bottom) {
                HStack {
                    Button(action: {
                        onboardingController.finish()
                    }) {
                        Text("Skip")
                    }.buttonStyle(.borderless)
                    Spacer()
                }
                .frame(maxWidth: .infinity)
                VStack(alignment: .center) {
                    CtaButton(label: "Next", action: {
                        onboardingModel.advance(to: .mode)
                    })
                }
                .frame(maxWidth: .infinity)
                VStack {
                    Spacer()
                }.frame(maxWidth: .infinity, maxHeight: 1)
            }
        }
    }
}

struct PreviewOnboardingController: OnboardingController {
    func finish() {}
}

struct OnboardingWelcomeView_Previews: PreviewProvider {
    static var previews: some View {
        OnboardingWelcomeView(onboardingController: PreviewOnboardingController())
            .environmentObject(OnboardingViewModel())
    }
}
