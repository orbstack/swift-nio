//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

struct OnboardingWelcomeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel

    var body: some View {
        VStack {
            Text("Welcome to MacVirt!")
                    .font(.title)
                    .padding(.bottom, 8)
            Text("Fast, secure, and easy to use virtualization for macOS.")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
                    .padding(.bottom, 8)

            Spacer()

            VStack {
                Text("MacVirt is a virtualization solution for macOS.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .padding(.bottom, 8)
                Text("It is built on top of Docker and QEMU.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .padding(.bottom, 8)
                Text("It is fast, secure, and easy to use.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .padding(.bottom, 8)
            }

            Spacer()

            Button(action: {
            }) {
                Text("Next")
            }
        }
    }
}