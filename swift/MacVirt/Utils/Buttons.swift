//
// Created by Danny Lin on 6/10/23.
//

import Foundation
import SwiftUI

struct ProgressIconButton: View {
    let systemImage: String
    let actionInProgress: Bool
    let role: ButtonRole?
    let action: () -> Void

    init(systemImage: String, actionInProgress: Bool, role: ButtonRole? = nil, action: @escaping () -> Void) {
        self.systemImage = systemImage
        self.actionInProgress = actionInProgress
        self.role = role
        self.action = action
    }

    var body: some View {
        Button(role: role, action: action) {
            // 0.7 scale crashes on macOS 12 - 0.75 is ok
            ZStack {
                if actionInProgress {
                    ProgressView()
                    .scaleEffect(0.75)
                } else {
                    Image(systemName: systemImage)
                }
            }.frame(maxWidth: 24, maxHeight: 24)
        }
        .buttonStyle(.borderless)
        // fallback case, but callers should still pass .disabled override
        // because button progress is per-action-type (start/stop/remove),
        // while disabled is for *any* action on the entity
        .disabled(actionInProgress)
    }
}