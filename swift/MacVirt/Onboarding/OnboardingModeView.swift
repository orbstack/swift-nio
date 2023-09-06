//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI
import Defaults

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

private struct ModeButton: View {
    private static let radius = 8.0
    @Environment(\.colorScheme) private var colorScheme: ColorScheme

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
            .background(Color.primary.opacity(hoverOpacity * 0.025), in: RoundedRectangle(cornerRadius: Self.radius))
            .background(Color.white.opacity(colorScheme == .dark ? 0.1 : 0.5), in: RoundedRectangle(cornerRadius: Self.radius))
            .cornerRadius(Self.radius)
            /*.overlay(
                RoundedRectangle(cornerRadius: Self.radius)
                    .stroke(Color.primary.opacity(0.1 + 0.15 * hoverOpacity), lineWidth: 1)
            )*/
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

struct OnboardingModeView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var onboardingModel: OnboardingViewModel

    let onboardingController: OnboardingController
    @Default(.selectedTab) private var rootSelectedTab

    var body: some View {
        VStack {
            Text("What do you want to use?")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("Don’t worry, you can always change this later and pick both.")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            HStack(spacing: 24) {
                ModeButton(
                    image: "distro_docker",
                    title: "Docker",
                    desc: "Build & run Docker containers",
                    action: {
                        rootSelectedTab = "docker"
                        continueWith(.docker)
                    }
                )

                ModeButton(
                    image: "distro_k8s",
                    title: "Kubernetes",
                    desc: "Test Kubernetes deployments",
                    action: {
                        rootSelectedTab = "k8s-pods"
                        continueWith(.k8s)
                    }
                )

                ModeButton(
                    image: "distro_ubuntu",
                    title: "Linux",
                    // match line count
                    desc: "Use a full Linux system\n ",
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
