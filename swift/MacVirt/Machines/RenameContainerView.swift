//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct RenameContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State var name: String
    @State private var nameChanged = false
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0

    var record: ContainerRecord
    @Binding var isPresented: Bool

    var body: some View {
        VStack(alignment: .leading) {
            Text("Rename “\(record.name)”")
                .font(.headline.weight(.semibold))
                .padding(.bottom, 8)

            Form {
                Section {
                    let nameBinding = Binding<String>(get: { name }, set: {
                        if $0 != name {
                            self.nameChanged = true
                        }
                        self.name = $0
                    })

                    TextField("Name", text: nameBinding)
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
                    isPresented = false
                }) {
                    Text("Cancel")
                }
                .keyboardShortcut(.cancelAction)

                Button(action: {
                    create()
                }) {
                    Text("Rename")
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
            name = record.name
            checkName(name, animate: false)
        }
        .onChange(of: vmModel.containers) { _ in
            checkName(name)
        }
    }

    private func checkName(_ newName: String, animate: Bool = true) {
        if let containers = vmModel.containers,
           containers.contains(where: { $0.name == newName && $0.id != record.id })
        {
            // renaming to same isn't considered duplicate
            isNameDuplicate = true
        } else {
            isNameDuplicate = false
        }

        // regex
        let isValid = containerNameRegex.firstMatch(in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count)) != nil &&
            !containerNameBlacklist.contains(newName)
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
        if isNameDuplicate || isNameInvalid || name.isEmpty {
            return
        }

        Task { @MainActor in
            await vmModel.tryRenameContainer(record, newName: name)
        }
        isPresented = false
    }
}
