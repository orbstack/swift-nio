//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

struct OnboardingRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject var onboardingModel = OnboardingViewModel()

    var body: some View {
        Group {
            OnboardingWelcomeView()
                    .environmentObject(onboardingModel)
        }
    }
}