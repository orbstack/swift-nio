//
// Created by Danny Lin on 12/25/23.
//

import Combine
import Foundation
import SwiftUI

class MainNavViewModel: ObservableObject {
    @Published var inspectorSelection = Set<AnyHashable>()
    @Published var inspectorView: EquatableViewContainer?

    let expandInspector = PassthroughSubject<Void, Never>()
}
