//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI
import Defaults

struct VisualEffectView: NSViewRepresentable {
    let blendingMode: NSVisualEffectView.BlendingMode
    let state: NSVisualEffectView.State
    let material: NSVisualEffectView.Material

    init(blendingMode: NSVisualEffectView.BlendingMode = .behindWindow,
         state: NSVisualEffectView.State = .followsWindowActiveState,
         material: NSVisualEffectView.Material = .underWindowBackground) {
        self.blendingMode = blendingMode
        self.state = state
        self.material = material
    }

    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()

        view.blendingMode = blendingMode
        view.state = state
        view.material = material

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
            }.padding()
            Spacer()
        }
        .frame(width: 650, height: 450)
        .background(VisualEffectView().ignoresSafeArea())
        .background(WindowAccessor(holder: windowHolder))
        .onChange(of: windowHolder.window) { window in
            if let window {
                window.isRestorable = false
            }
        }
        .onAppear {
            if let window = windowHolder.window {
                window.isRestorable = false
            }
        }
    }

    func finish() {
        onboardingCompleted = true
        windowHolder.window?.close()
        //NSWorkspace.shared.open(URL(string: "orbstack://main")!)
    }
}
