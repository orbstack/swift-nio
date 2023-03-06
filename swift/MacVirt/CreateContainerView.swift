//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

fileprivate let containerNamePattern = (try? NSRegularExpression(pattern: "^[a-zA-Z0-9_-]+$"))!

struct CreateContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = "ubuntu"
    @State private var nameChanged = false
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0
    #if arch(arm64)
    @State private var arch = "arm64"
    #else
    @State private var arch = "amd64"
    #endif
    @State private var distro = Distro.ubuntu

    @Binding var isPresented: Bool
    @Binding var creatingCount: Int

    var body: some View {
        Form {
            Section {
                let nameBinding = Binding<String>(get: { name }, set: {
                    if $0 != name {
                        self.nameChanged = true
                    }
                    self.name = $0
                })

                TextField("Name", text: nameBinding)
                let errorText = isNameInvalid ? "Invalid name" : "Already exists"
                Text(errorText)
                        .font(.caption)
                        .foregroundColor(.red)
                        .frame(maxHeight: duplicateHeight)
                        .clipped()

                Picker("Distribution", selection: $distro) {
                    ForEach(Distro.allCases, id: \.self) { distro in
                        Text(distro.friendlyName).tag(distro)
                    }
                }
                #if arch(arm64)
                if #available(macOS 13, *) {
                    if vmModel.config?.rosetta ?? true {
                        Picker("CPU type", selection: $arch) {
                            Text("Apple").tag("arm64")
                            Text("Intel").tag("amd64")
                        }
                                .pickerStyle(.segmented)
                                .disabled(distro == .nixos)
                    }
                }
                #endif

                Button(action: {
                    Task { @MainActor in
                        creatingCount += 1
                        await vmModel.tryCreateContainer(name: name, distro: distro, arch: arch)
                        creatingCount -= 1
                    }
                    isPresented = false
                }) {
                    Text("Create")
                }.keyboardShortcut(.defaultAction)
                // empty is disabled but not error
                .disabled(isNameDuplicate || isNameInvalid || name.isEmpty)
            }
        }
        .padding(16)
        .onChange(of: distro) {
            if !nameChanged {
                name = $0.rawValue
            }

            #if arch(arm64)
            // NixOS doesn't work with Rosetta
            if $0 == .nixos {
                arch = "arm64"
            }
            #endif
        }
        .onChange(of: name) { newName in
            if let containers = vmModel.containers,
                    containers.contains(where: { $0.name == newName }) {
                isNameDuplicate = true
            } else {
                isNameDuplicate = false
            }

            // regex
            if !newName.isEmpty && containerNamePattern.firstMatch(in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count)) == nil {
                isNameInvalid = true
            } else {
                isNameInvalid = false
            }
        }
        .onChange(of: isNameDuplicate || isNameInvalid) { hasError in
            if hasError {
                withAnimation(.spring()) {
                    duplicateHeight = NSFont.preferredFont(forTextStyle: .caption1).pointSize
                }
            } else {
                withAnimation(.spring()) {
                    duplicateHeight = 0
                }
            }
        }
    }
}