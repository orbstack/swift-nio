//
// Created by Danny Lin on 2/2/24.
//

import AppKit
import Foundation
import SwiftUI

struct LicenseBadgeView: View {
    @ObservedObject var vmModel: VmViewModel

    var body: some View {
        // "Personal use only" and "Sign in again"
        if vmModel.drmState.statusDotColor == .red {
            Text(vmModel.drmState.subtitle)
                .font(.caption)
                .padding(.vertical, 4)
                .padding(.horizontal, 8)
                // opacity 0.5 = can see divider moving through when expanding/collapsing sidebar
                .background(.thinMaterial)
                .background(Color.red.opacity(0.5))
                .onTapGesture {
                    if vmModel.drmState.subtitle == "Sign in again" {
                        vmModel.presentAuth = true
                    } else {
                        NSWorkspace.shared.open(URL(string: "https://orbstack.dev/pricing")!)
                    }
                }
                .clipShape(Capsule())
        } else {
            EmptyView()
        }
    }
}
