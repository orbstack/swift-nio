//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

private enum OnboardingMode {
    case docker
    case linux
}

fileprivate struct ModeButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
                .padding()
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                .cornerRadius(6.0)
                .padding()
    }
}

fileprivate struct ModeButton: View {
    let image: String
    let title: String
    let desc: String
    let action: () -> Void

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
                Text(title)
                        .font(.title3)
                        .bold()
                        .padding(.bottom, 8)
                Text(desc)
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .padding(.bottom, 8)
            }.padding()
        }.buttonStyle(ModeButtonStyle())
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct OnboardingModeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    let onboardingController: OnboardingController
    @AppStorage("root.selectedTab") private var rootSelectedTab = "machines"

    var body: some View {
        VStack {
            Text("What do you want to run?")
                    .font(.largeTitle)
                    .padding(.bottom, 16)
                    .padding(.top, 16)
            Text("Let’s get you up and running. Don’t worry, you can always change this later and run both Linux and Docker.")
                    .font(.body.weight(.medium))
                    .foregroundColor(.secondary)
                    .padding(.bottom, 8)

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
                    desc: "Run full Linux systems",
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