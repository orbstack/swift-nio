//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI
import Defaults

private enum OnboardingMode {
    case docker
    case linux
}

fileprivate struct DummyButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
    }
}

fileprivate struct ModeButton: View {
    private static let radius = 8.0

    let image: String
    let title: String
    let desc: String
    let action: () -> Void
    
    @State private var hoverOpacity = 0.0
    @State private var activeOpacity = 0.0

    init(image: String, title: String, desc: String, action: @escaping () -> Void) {
        self.image = image
        self.title = title
        self.desc = desc
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            VStack {
                Image(image)
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 48, height: 48)
                        .padding(.bottom, 8)
                Text(title)
                        .font(.title3)
                        .fontWeight(.medium)
                        .multilineTextAlignment(.center)
                        .padding(.bottom, 2)
                Text(desc)
                        .font(.body)
                        .foregroundColor(.secondary)
                        .multilineTextAlignment(.center)
            }
            .padding(16)
            .frame(width: 175, height: 175)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: Self.radius))
            .background(Color.primary.opacity(hoverOpacity * 0.1), in: RoundedRectangle(cornerRadius: Self.radius))
            .cornerRadius(Self.radius)
            .overlay(
                RoundedRectangle(cornerRadius: Self.radius)
                    .stroke(Color.primary.opacity(0.1 + 0.15 * hoverOpacity), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
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
        .padding()
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct OnboardingModeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    let onboardingController: OnboardingController
    @Default(.selectedTab) private var rootSelectedTab

    var body: some View {
        VStack {
            Text("What do you want to run?")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("Don’t worry, you can always change this later and run both Linux and Docker.")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            HStack {
                ModeButton(
                    image: "distro_docker",
                    title: "Docker",
                    desc: "Build and run Docker containers",
                    action: {
                        rootSelectedTab = "docker"
                        continueWith(.docker)
                    }
                )

                ModeButton(
                    image: "distro_ubuntu",
                    title: "Linux",
                    desc: "Run full Linux systems\n ",
                    action: {
                        rootSelectedTab = "machines"
                        continueWith(.linux)
                    }
                )
            }.fixedSize()

            Spacer()

            HStack {
                Button(action: {
                    onboardingModel.back()
                }) {
                    Text("Back")
                }
                .buttonStyle(.borderless)
                Spacer()
            }
        }
    }

    private func continueWith(_ mode: OnboardingMode) {
        switch mode {
        case .docker:
            onboardingController.finish()
        case .linux:
            onboardingModel.advance(to: .create)
        }
    }
}

struct OnboardingModeView_Previews: PreviewProvider {
    static var previews: some View {
        OnboardingModeView(onboardingController: PreviewOnboardingController())
            .environmentObject(OnboardingViewModel())
    }
}
