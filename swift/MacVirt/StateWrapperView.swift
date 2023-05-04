//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct StateWrapperView<Content: View>: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        switch vmModel.state {
        case .stopped:
            VStack {
                Text("Not running")
                        .font(.title)
                        .foregroundColor(.secondary)
                Button(action: {
                    Task {
                        await vmModel.tryStartAndWait()
                    }
                }) {
                    Text("Start")
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
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