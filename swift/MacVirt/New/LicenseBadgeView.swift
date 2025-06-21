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
                .padding(.vertical, useFatToolbar ? 12 : 4)
                .padding(.horizontal, useFatToolbar ? 12 : 8)
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

    // if OS >= 26 and compiled with Xcode 26 SDK, match fat toolbar capsule padding
    private var useFatToolbar: Bool {
        // compile SDK check
        #if canImport(FoundationModels)
            // runtime SDK check
            if #available(macOS 26, *) {
                true
            } else {
                false
            }
        #else
            false
        #endif
    }
}
