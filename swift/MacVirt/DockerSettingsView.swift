//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct DockerSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @State private var memoryMib = 0.0
    @State private var cpu = 1.0
    @State private var enableRosetta = true
    @State private var enableRosettaFalse = false

    var body: some View {
        Form {
            Button("Edit engine config") {
                let path = "\(Folders.config)/docker.json"
                NSWorkspace.shared.open(URL(fileURLWithPath: path))
            }

            if vmModel.state == .running,
               let machines = vmModel.containers,
               let dockerRecord = machines.first(where: { $0.builtin && $0.name == "docker" }) {
                Button("Restart Docker") {
                    Task {
                        await vmModel.tryRestartContainer(dockerRecord)
                    }
                }
            }
        }
        .padding()
        .navigationTitle("Settings")
    }
}
