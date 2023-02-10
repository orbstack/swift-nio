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

protocol OnboardingController {
    func finish()
}

struct OnboardingRootView: View, OnboardingController {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject var onboardingModel = OnboardingViewModel()
    @AppStorage("onboardingCompleted") var onboardingCompleted = false
    @StateObject private var windowHolder = WindowHolder()

    var body: some View {
        HStack {
            Spacer()
            VStack {
                Spacer()
                Group {
                    switch onboardingModel.step {
                    case .welcome:
                        OnboardingWelcomeView(onboardingController: self)
                    case .mode:
                        OnboardingModeView(onboardingController: self)
                    case .create:
                        OnboardingCreateView(onboardingController: self)
                    }
                }
                        .environmentObject(onboardingModel)
                Spacer()
            }.padding()
            Spacer()
        }
        .frame(width: 650, height: 450)
        .background(VisualEffectView().ignoresSafeArea())
        .background(WindowAccessor(holder: windowHolder))
    }

    func finish() {
        onboardingCompleted = true
        windowHolder.window?.close()
        NSWorkspace.shared.open(URL(string: "macvirt://main")!)
    }
}
