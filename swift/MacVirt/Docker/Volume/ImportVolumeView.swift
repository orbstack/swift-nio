//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern =
    (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct ImportVolumeView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var actionTracker: ActionTracker

    @State private var name = ""

    var body: some View {
        CreateForm {
            Section("Import Volume") {
                ValidatedTextField(
                    "Name", text: $name,
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
            }

            CreateButtonRow {
                Button {
                    vmModel.presentImportVolume = nil
                } label: {
                    Text("Cancel")
                }
                .keyboardShortcut(.cancelAction)

                CreateSubmitButton("Import")
                    .keyboardShortcut(.defaultAction)
            }
        } onSubmit: {
            let url = vmModel.presentImportVolume!
            Task { @MainActor in
                // TODO: figure out how to support changing AKList selection so that we can just select + scroll to the new volume
                await actionTracker.with(volumeId: name, action: .importing) {
                    await vmModel.tryDockerImportVolume(url: url, newName: name)
                }
            }
            vmModel.presentImportVolume = nil
        }
        .onAppear {
            do {
                let config: ExportedVolumeConfigV1 = try Zstd.readSkippableFrame(
                    url: vmModel.presentImportVolume!,
                    expectedVersion: ZstdFrameVersion.dockerVolumeConfig1)
                name = config.name
            } catch {
                NSLog("Failed to read config from export: \(error)")
            }
        }
    }
}
