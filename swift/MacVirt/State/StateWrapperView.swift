//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct StateWrapperView<Content: View>: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @ViewBuilder let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        // special case: restarting is not really a state, but avoid flicker when restarting for settings
        if vmModel.isVmRestarting {
            ProgressView(label: {
                Text("Restarting")
            })
        } else {
            switch vmModel.state {
            case .stopped:
                VStack(spacing: 16) { // match ContentUnavailableViewCompat desc padding
                    ContentUnavailableViewCompat("Service Not Running", systemImage: "moon.zzz.fill")

                    Button(action: {
                        Task {
                            await vmModel.tryStartDaemon()
                        }
                    }) {
                        Text("Start")
                            .padding(.horizontal, 4)
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .controlSize(.large)
                }
            case .spawning:
                ProgressView(label: {
                    Text("Updating")
                })
            case .starting:
                ProgressView(label: {
                    Text("Starting")
                })
            case .running:
                content()
            case .stopping:
                ProgressView(label: {
                    Text("Stopping")
                })
            }
        }
    }
}
