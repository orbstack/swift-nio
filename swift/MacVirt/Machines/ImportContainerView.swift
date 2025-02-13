//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-.]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct ImportContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = ""
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Import Machine")
                .font(.headline.weight(.semibold))
                .padding(.bottom, 8)

            Form {
                Section {
                    TextField("Name", text: $name, prompt: Text("(keep original)"))
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
                    vmModel.presentImportMachine = nil
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
                .disabled(isNameDuplicate || isNameInvalid)
            }
            .padding(.top, 8)
        }
        .padding(20)
        .onChange(of: name) { newName in
            checkName(newName)
        }
        .onAppear {
            checkName(name, animate: false)
        }
        .onChange(of: vmModel.containers) { _ in
            checkName(name)
        }
    }

    private func checkName(_ newName: String, animate: Bool = true) {
        if let containers = vmModel.containers,
            containers.contains(where: { $0.record.name == newName })
        {
            isNameDuplicate = true
        } else {
            isNameDuplicate = false
        }

        // regex
        let isValid =
            containerNameRegex.firstMatch(
                in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count))
            != nil && !containerNameBlacklist.contains(newName)
        if !newName.isEmpty && !isValid {
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
        if isNameDuplicate || isNameInvalid {
            return
        }

        let url = vmModel.presentImportMachine!
        Task { @MainActor in
            await vmModel.tryImportContainer(url: url, newName: name.isEmpty ? nil : name)
        }
        vmModel.presentImportMachine = nil
    }
}
