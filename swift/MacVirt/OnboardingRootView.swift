//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

struct VisualEffectView: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()

        view.blendingMode = .behindWindow
        view.state = .active
        view.material = .underWindowBackground

        return view
    }

    func updateNSView(_ nsView: NSVisualEffectView, context: Context) {
    }
}

struct OnboardingRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject var onboardingModel = OnboardingViewModel()

    var body: some View {
        HStack {
            Spacer()
            VStack {
                Spacer()
                OnboardingWelcomeView()
                        .environmentObject(onboardingModel)
                Spacer()
            }
            Spacer()
        }
        .background(VisualEffectView().ignoresSafeArea())
    }
}