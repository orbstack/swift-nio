//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

struct OnboardingWelcomeView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    let onboardingController: OnboardingController

    var body: some View {
        VStack {
            Spacer()

            Image("AppIconUI")
                .resizable()
                .frame(width: 150, height: 150)
                .padding(.bottom, 24)

            Text("Welcome to OrbStack")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
            Text("Seamless and efficient Docker and Linux on your Mac")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            Text("By continuing, you agree to our [terms](https://orbstack.dev/terms) and [privacy policy](https://orbstack.dev/privacy).")
                .font(.subheadline)
                .foregroundColor(.secondary)
                .padding(.bottom, 16)

            HStack(alignment: .bottom) {
                HStack {
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
