//
// Created by Danny Lin on 2/6/23.
//

import Defaults
import Foundation
import SwiftUI

struct VisualEffectView: NSViewRepresentable {
    let blendingMode: NSVisualEffectView.BlendingMode
    let state: NSVisualEffectView.State
    let material: NSVisualEffectView.Material

    init(
        blendingMode: NSVisualEffectView.BlendingMode = .behindWindow,
        state: NSVisualEffectView.State = .followsWindowActiveState,
        material: NSVisualEffectView.Material = .underWindowBackground
    ) {
        self.blendingMode = blendingMode
        self.state = state
        self.material = material
    }

    func makeNSView(context _: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()

        view.blendingMode = blendingMode
        view.state = state
        view.material = material

        return view
    }

    func updateNSView(_: NSVisualEffectView, context _: Context) {}
}

protocol OnboardingController {
    func finish()
}

struct OnboardingRootView: View, OnboardingController {
    @Environment(\.openWindow) private var openWindow
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject var onboardingModel = OnboardingViewModel()
    @Default(.onboardingCompleted) var onboardingCompleted
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
            }.padding(32)
            Spacer()
        }
        .frame(width: 725, height: 550)
        .background(VisualEffectView().ignoresSafeArea())
        .windowHolder(windowHolder)
        .windowRestorability(false)
    }

    func finish() {
        onboardingCompleted = true
        windowHolder.window?.close()
        openWindow.call(id: WindowID.main)

        // ok to re-enable menu bar now
        Defaults[.globalShowMenubarExtra] = true
    }
}
