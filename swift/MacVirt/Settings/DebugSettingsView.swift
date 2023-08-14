//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle
import Defaults

struct DebugSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let updaterController: SPUStandardUpdaterController

    var body: some View {
        Form {
            Button(action: {
                Defaults[.onboardingCompleted] = false
            }) {
                Text("reset onboarding")
            }
            Button(action: {
                Defaults[.tipsMenubarBgShown] = false
                Defaults[.dockerMigrationDismissed] = false
            }) {
                Text("reset tips")
            }

            Spacer()
                .frame(height: 32)

            Text("Helper")
            //Text("Installed: \(vmModel.privHelper.checkInstalled() ? "yes" : "no")")
            Button("Install") {
                Task {
                    do {
                        try await vmModel.privHelper.install()
                    } catch {
                        print(error)
                    }
                }
            }
            Button("Uninstall") {
                Task {
                    do {
                        try await vmModel.privHelper.uninstall()
                    } catch {
                        print(error)
                    }
                }
            }
            Button("Update") {
                Task {
                    do {
                        try await vmModel.privHelper.update()
                    } catch {
                        print(error)
                    }
                }
            }
            Button("action: symlink") {
                Task {
                    do {
                        try await vmModel.privHelper.symlink(src: Files.dockerSocket, dest: "/var/run/docker.sock")
                    } catch {
                        print(error)
                    }
                }
            }
        }
        .padding()
    }
}
