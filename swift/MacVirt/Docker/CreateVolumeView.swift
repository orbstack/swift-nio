//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern = (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct CreateVolumeView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = ""
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0

    @Binding var isPresented: Bool

    var body: some View {
        VStack(alignment: .leading) {
            Text("New Volume")
                .font(.headline.weight(.semibold))
                .padding(.bottom, 8)

            Form {
                Section {
                    let nameBinding = Binding<String>(get: { name }, set: {
                        self.name = $0
                    })

                    TextField("Name", text: nameBinding)
                        .onSubmit {
                            submit()
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
                    submit()
                }) {
                    Text("Create")
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
        if !newName.isEmpty && dockerRestrictedNamePattern.firstMatch(in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count)) == nil {
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

    private func submit() {
        if isNameDuplicate || isNameInvalid || name.isEmpty {
            return
        }

        Task { @MainActor in
            await vmModel.tryDockerVolumeCreate(name)
        }
        isPresented = false
    }
}
