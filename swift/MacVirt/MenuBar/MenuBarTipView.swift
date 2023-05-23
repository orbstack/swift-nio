//
// Created by Danny Lin on 5/22/23.
//

import Foundation
import SwiftUI

struct MenuBarTipView: View {
    var onClose: () -> Void

    var body: some View {
        HStack {
            VStack {
                Text("You can always find OrbStack here")
            }
        }
        .padding()
        .onTapGesture {
            onClose()
        }
    }
}