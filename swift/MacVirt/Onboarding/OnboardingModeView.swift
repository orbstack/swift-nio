//
// Created by Danny Lin on 2/6/23.
//

import Defaults
import Foundation
import SwiftUI

private enum OnboardingMode {
    case docker
    case k8s
    case linux
}

private struct DummyButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
    }
}

private let radius = 8.0

private struct ModeButton<ImageView: View>: View {
    @Environment(\.colorScheme) private var colorScheme: ColorScheme

    @ViewBuilder let image: () -> ImageView
    let title: String
    let action: () -> Void

    @State private var hoverOpacity = 0.0
    @State private var activeOpacity = 0.0

    // init(image: String, title: String, action: @escaping () -> Void) {
    //     self.image = {
    //         Image(image)
    //             .resizable()
    //             .aspectRatio(contentMode: .fit)
    //             .frame(width: 48, height: 48)
    //     }
    //     self.title = title
    //     self.action = action
    // }

    init(_ title: String, action: @escaping () -> Void, @ViewBuilder image: @escaping () -> ImageView) {
        self.title = title
        self.image = image
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            VStack {
                image()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 48, height: 48)
                    .padding(.bottom, 8)
                Text(title)
                    .font(.title3)
                    .fontWeight(.medium)
                    .multilineTextAlignment(.center)
                    .padding(.bottom, 2)
            }
            .padding(16)
            .frame(width: 175, height: 175)
            .background(
                Color.primary.opacity(hoverOpacity * 0.025),
                in: RoundedRectangle(cornerRadius: radius)
            )
            .background(
                Color.white.opacity(colorScheme == .dark ? 0.1 : 0.5),
                in: RoundedRectangle(cornerRadius: radius)
            )
            .cornerRadius(radius)
            /* .overlay(
                 RoundedRectangle(cornerRadius: radius)
                     .stroke(Color.primary.opacity(0.1 + 0.15 * hoverOpacity), lineWidth: 1)
             ) */
            .shadow(color: Color.primary.opacity(0.1), radius: 2, x: 0, y: 1)
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
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

extension ModeButton where ImageView == Image {
    init(_ title: String, image: String, action: @escaping () -> Void) {
        self.init(title, action: action, image: {
            Image(image)
                .resizable()
        })
    }
}

struct OnboardingModeView: View {
    @Default(.selectedTab) private var selectedTab

    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var onboardingModel: OnboardingViewModel

    let onboardingController: OnboardingController

    var body: some View {
        VStack {
            Text("What do you want to use?")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("Don’t worry, you can always change this later.")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            HStack(spacing: 24) {
                ModeButton("Docker") {
                    selectedTab = .dockerContainers
                    continueWith(.docker)
                } image: {
                    // yay trademarks
                    DockerImageIconPlaceholder(id: "docker")
                        .scaleEffect(48/28)
                }

                ModeButton("Kubernetes", image: "distro/k8s") {
                    selectedTab = .k8sPods
                    continueWith(.k8s)
                }

                ModeButton("Linux", image: "distro/ubuntu") {
                    selectedTab = .machines
                    continueWith(.linux)
                }
            }.fixedSize()

            Spacer()

            HStack {
                Button {
                    onboardingModel.back()
                } label: {
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
        case .k8s:
            Task { @MainActor in
                // wait for a config
                await vmModel.waitForNonNil(\.$config)
                // enable k8s as soon as possible
                await vmModel.tryStartStopK8s(enable: true, force: true)
            }

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
