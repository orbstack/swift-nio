//
// Created by Danny Lin on 2/5/23.
//

import AppKit
import Foundation
import SwiftUI
import UniformTypeIdentifiers

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern =
    (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct CreateVolumeView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()

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
                Button {
                    let panel = NSOpenPanel()
                    panel.canChooseFiles = true
                    // ideally we can filter for .tar.zst but that's not possible :(
                    panel.allowedContentTypes = [UTType(filenameExtension: "zst", conformingTo: .data)!]
                    panel.canChooseDirectories = false
                    panel.canCreateDirectories = false
                    panel.message = "Select volume (.tar.zst) to import"

                    guard let window = windowHolder.window else { return }
                    panel.beginSheetModal(for: window) { result in
                        if result == .OK,
                            let url = panel.url
                        {
                            isPresented = false
                            vmModel.presentImportVolume = url
                        }
                    }
                } label: {
                    Text("Import from File…")
                }

                Spacer()

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
        .windowHolder(windowHolder)
    }
}
