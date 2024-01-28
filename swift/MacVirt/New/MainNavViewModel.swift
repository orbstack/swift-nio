//
// Created by Danny Lin on 12/25/23.
//

import Foundation
import Combine

class MainNavViewModel: ObservableObject {
    @Published var inspectorContents: InspectorWrapper? = nil

    let expandInspector = PassthroughSubject<Void, Never>()
}
