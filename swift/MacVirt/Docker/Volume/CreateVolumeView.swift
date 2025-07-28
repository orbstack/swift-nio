//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern =
    (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct CreateVolumeView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = ""

    @Binding var isPresented: Bool

    var body: some View {
        CreateForm {
            Section {
                let nameBinding = Binding<String>(
                    get: { name },
                    set: {
                        self.name = $0
                    })

                ValidatedTextField(
                    "Name", text: nameBinding,
                    validate: { value in
                        // duplicate
                        if vmModel.dockerVolumes?[value] != nil {
                            return "Already exists"
                        }

                        // regex
                        if dockerRestrictedNamePattern.firstMatch(
                            in: value, options: [],
                            range: NSRange(location: 0, length: value.utf16.count))
                            == nil
                        {
                            return "Invalid name"
                        }

                        return nil
                    })
            } header: {
                Text("New Volume")
                Text(
                    "Volumes are for sharing data between containers. Unlike bind mounts, they are stored on a native Linux file system, making them faster and more reliable."
                )
            }

            CreateButtonRow {
                HelpButton {
                    NSWorkspace.shared.open(
                        URL(string: "https://orb.cx/docker-docs/volume-create")!)
                }

                Button {
                    isPresented = false
                } label: {
                    Text("Cancel")
                }
                .keyboardShortcut(.cancelAction)

                CreateSubmitButton("Create")
                    .keyboardShortcut(.defaultAction)
            }
        } onSubmit: {
            Task { @MainActor in
                await vmModel.tryDockerVolumeCreate(name)
            }
            isPresented = false
        }
    }
}
