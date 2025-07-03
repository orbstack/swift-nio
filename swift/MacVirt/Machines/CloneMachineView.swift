//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct CloneMachineView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State var name: String

    var record: ContainerRecord
    @Binding var isPresented: Bool

    var body: some View {
        CreateForm {
            Section("Clone \"\(record.name)\"") {
                ValidatedTextField(
                    "Name", text: $name,
                    validate: { value in
                        // duplicate
                        if let containers = vmModel.machines,
                            containers.values.contains(where: {
                                $0.record.name == value && $0.record.id != record.id
                            })
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
                    isPresented = false
                } label: {
                    Text("Cancel")
                }
                .keyboardShortcut(.cancelAction)

                CreateSubmitButton("Clone")
                    .keyboardShortcut(.defaultAction)
            }
        } onSubmit: {
            Task { @MainActor in
                await vmModel.tryCloneContainer(record, newName: name)
            }
            isPresented = false
        }
        .onAppear {
            name = record.name + "2"
        }
    }
}
