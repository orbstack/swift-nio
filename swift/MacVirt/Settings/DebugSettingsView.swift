//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Defaults
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct DebugSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let updaterController: SPUStandardUpdaterController

    var body: some View {
        SettingsForm {
            Section {
                Button {
                    Defaults[.onboardingCompleted] = false
                } label: {
                    Text("reset onboarding")
                }

                Button {
                    Defaults[.tipsMenubarBgShown] = false
                    Defaults[.tipsContainerDomainsShow] = true
                    Defaults[.tipsContainerFilesShow] = true
                    Defaults[.tipsImageMountsShow] = true
                    Defaults[.dockerMigrationDismissed] = false
                } label: {
                    Text("reset tips")
                }
            } header: {
                Text("UserDefaults")
            }

            Section {
                Button("action: symlink") {
                    Task {
                        vmModel.privHelper.installReason = "Test?"
                        do {
                            try await vmModel.privHelper.symlink(
                                src: Files.dockerSocket, dest: "/var/run/docker.sock")
                        } catch {
                            print(error)
                        }
                    }
                }
            } header: {
                Text("Privileged Helper")
            }
        }
        .akNavigationTitle("Debug")
    }
}
