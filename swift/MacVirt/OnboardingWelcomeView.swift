//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

struct CtaButton: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
                .padding(.vertical, 8)
                .padding(.horizontal, 16)
                .background(configuration.isPressed ? Color.accentColor : Color.accentColor)
                .foregroundColor(.primary)
                .cornerRadius(6.0)
    }
}

struct OnboardingWelcomeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    let onboardingController: OnboardingController

    var body: some View {
        VStack {
            Text(Constants.userAppName)
                    .font(.largeTitle)
                    .padding(.bottom, 16)
                    .padding(.top, 16)
            Text("Fast, light, simple Linux machines and containers")
                    .font(.body.weight(.medium))
                    .foregroundColor(.secondary)
                    .padding(.bottom, 8)

            Spacer()

            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Image(systemName: "bolt.fill")
                        .resizable()
                        .frame(width: 32, height: 32)
                        .foregroundColor(.orange)
                    Text("Fast.")
                        .font(.headline)
                    Text("Fast network and disk performance, starts fast")
                            .font(.body)
                            .foregroundColor(.secondary)
                    Spacer()
                }.padding(.horizontal)
                .frame(maxWidth: .infinity)
                HStack {
                    Image(systemName: "wind.circle.fill")
                        .resizable()
                        .frame(width: 32, height: 32)
                        .foregroundColor(.blue)
                    Text("Light.")
                            .font(.headline)
                    Text("Native app, low CPU usage,")
                            .font(.body)
                            .foregroundColor(.secondary)
                    Spacer()
                }.padding(.horizontal)
                .frame(maxWidth: .infinity)
                HStack {
                    Image(systemName: "checkmark.circle.fill")
                        .resizable()
                        .frame(width: 32, height: 32)
                        .foregroundColor(.green)
                    Text("Simple.")
                            .font(.headline)
                    Text("Easy to use, no configuration required")
                            .font(.body)
                            .foregroundColor(.secondary)
                    Spacer()
                }.padding(.horizontal)
                .frame(maxWidth: .infinity)
            }.fixedSize()

            Spacer()

            HStack(alignment: .center) {
                Button(action: {
                    onboardingController.finish()
                }) {
                    Text("Skip")
                }.buttonStyle(.borderless)
                Spacer()
                Button(action: {
                    onboardingModel.advance(to: .mode)
                }) {
                    Text("Continue")
                }
                .buttonStyle(CtaButton())
                .keyboardShortcut(.defaultAction)
                Spacer()
            }
        }
    }
}
