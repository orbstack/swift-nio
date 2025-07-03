//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-.]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct ImportMachineView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Import Machine")
                .font(.headline.weight(.semibold))
                .padding(.bottom, 8)

            CreateForm {
                Section {
                    ValidatedTextField(
                        "Name", text: $name, prompt: Text("(keep original)"),
                        validate: { value in
                            // empty is allowed for import (keeps original name)
                            if value.isEmpty {
                                return nil
                            }

                            // duplicate
                            if let containers = vmModel.machines,
                                containers.values.contains(where: { $0.record.name == value })
                            {
                                return "Already exists"
                            }

                            // regex
                            let isValid =
                                containerNameRegex.firstMatch(
                                    in: value, options: [],
                                    range: NSRange(location: 0, length: value.utf16.count))
                                != nil && !containerNameBlacklist.contains(value)
                            if !isValid {
                                return "Invalid name"
                            }

                            return nil
                        })
                }

                CreateButtonRow {
                    Button {
                        vmModel.presentImportMachine = nil
                    } label: {
                        Text("Cancel")
                    }
                    .keyboardShortcut(.cancelAction)

                    CreateSubmitButton("Import")
                        .keyboardShortcut(.defaultAction)
                }
            } onSubmit: {
                let url = vmModel.presentImportMachine!
                Task { @MainActor in
                    await vmModel.tryImportContainer(url: url, newName: name.isEmpty ? nil : name)
                }
                vmModel.presentImportMachine = nil
            }
        }
        .padding(20)
        .onAppear {
            do {
                let config: ExportedMachineV1 = try Zstd.readSkippableFrame(
                    url: vmModel.presentImportMachine!,
                    expectedVersion: ZstdFrameVersion.machineConfig1)
                name = config.record.name
            } catch {
                NSLog("Failed to read config from export: \(error)")
            }
        }
    }
}
