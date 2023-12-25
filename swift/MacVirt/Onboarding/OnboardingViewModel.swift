//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

enum OnboardingStep {
    case welcome
    case mode
    case create
}

class OnboardingViewModel: ObservableObject {
    @Published private(set) var step: OnboardingStep = .welcome

    func advance(to target: OnboardingStep) {
        step = target
    }

    func back() {
        switch step {
        case .welcome:
            break
        case .mode:
            step = .welcome
        case .create:
            step = .mode
        }
    }
}
