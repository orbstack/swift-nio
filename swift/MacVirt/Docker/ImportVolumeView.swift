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
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Import Volume")
                .font(.headline.weight(.semibold))
                .padding(.bottom, 8)

            Form {
                Section {
                    TextField("Name", text: $name)
                        .onSubmit {
                            create()
                        }

                    let errorText = isNameInvalid ? "Invalid name" : "Already exists"
                    Text(errorText)
                        .font(.caption)
                        .foregroundColor(.red)
                        .frame(maxHeight: duplicateHeight)
                        .clipped()
                }
            }

            HStack {
                Spacer()
                Button(action: {
                    vmModel.presentImportVolume = nil
                }) {
                    Text("Cancel")
                }
                .keyboardShortcut(.cancelAction)

                Button(action: {
                    create()
                }) {
                    Text("Import")
                }
                .keyboardShortcut(.defaultAction)
                // empty is disabled but not error
                .disabled(isNameDuplicate || isNameInvalid || name.isEmpty)
            }
            .padding(.top, 8)
        }
        .padding(20)
        .onChange(of: name) { newName in
            checkName(newName)
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

            checkName(name, animate: false)
        }
        .onChange(of: vmModel.dockerVolumes) { _ in
            checkName(name)
        }
    }

    private func checkName(_ newName: String, animate: Bool = true) {
        if let volumes = vmModel.dockerVolumes,
            volumes.contains(where: { $0.name == newName })
        {
            isNameDuplicate = true
        } else {
            isNameDuplicate = false
        }

        // regex
        if !newName.isEmpty
            && dockerRestrictedNamePattern.firstMatch(
                in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count))
                == nil
        {
            isNameInvalid = true
        } else {
            isNameInvalid = false
        }

        let hasError = isNameDuplicate || isNameInvalid
        let animation = animate ? Animation.spring() : nil
        if hasError {
            withAnimation(animation) {
                duplicateHeight = NSFont.preferredFont(forTextStyle: .caption1).pointSize
            }
        } else {
            withAnimation(animation) {
                duplicateHeight = 0
            }
        }
    }

    private func create() {
        // disabled
        if isNameDuplicate || isNameInvalid || name.isEmpty {
            return
        }

        let url = vmModel.presentImportVolume!
        Task { @MainActor in
            // TODO: figure out how to support changing AKList selection so that we can just select + scroll to the new volume
            await actionTracker.with(volumeId: name, action: .importing) {
                await vmModel.tryDockerImportVolume(url: url, newName: name)
            }
        }
        vmModel.presentImportVolume = nil
    }
}
