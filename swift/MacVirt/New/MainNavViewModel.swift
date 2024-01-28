//
// Created by Danny Lin on 12/25/23.
//

import Combine
import Foundation

class MainNavViewModel: ObservableObject {
    @Published var inspectorSelection = Set<AnyHashable>()

    let expandInspector = PassthroughSubject<Void, Never>()
}
