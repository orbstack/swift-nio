//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import UniformTypeIdentifiers

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-.]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

private enum FileItem: Hashable {
    case none
    case file(URL)
    case other
}

struct CreateContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var name = "ubuntu"
    @State private var nameChanged = false
    #if arch(arm64)
        @State private var arch = "arm64"
    #else
        @State private var arch = "amd64"
    #endif
    @State private var distro = Distro.ubuntu
    @State private var version = Distro.ubuntu.versions.last!.key
    @State private var cloudInitFile: URL? = nil
    @State private var defaultUsername = ""

    @Binding var isPresented: Bool

    var body: some View {
        CreateForm {
            Section("New Machine") {
                let nameBinding = Binding<String>(
                    get: { name },
                    set: {
                        if $0 != name {
                            self.nameChanged = true
                        }
                        self.name = $0
                    })

                ValidatedTextField(
                    "Name", text: nameBinding,
                    validate: { value in
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

            Section {
                Picker("Distribution", selection: $distro) {
                    ForEach(Distro.allCases, id: \.self) { distro in
                        Text(distro.friendlyName)
                    }
                }

                Picker("Version", selection: $version) {
                    ForEach(distro.versions, id: \.self) { version in
                        if version == distro.versions.last! && distro.versions.count > 1 {
                            Divider()
                        }
                        Text(version.friendlyName).tag(version.key)
                    }
                }.disabled(distro.versions.count == 1)

                #if arch(arm64)
                    Picker("Architecture", selection: $arch) {
                        Text("arm64").tag("arm64")
                        Text("x86-64 (Intel, emulated)").tag("amd64")
                    }
                    .disabled(distro == .nixos)
                #endif
            }

            Section("Advanced") {
                let userDataBinding = Binding<FileItem> {
                    if let cloudInitFile {
                        return FileItem.file(cloudInitFile)
                    } else {
                        return FileItem.none
                    }
                } set: {
                    switch $0 {
                    case .none:
                        cloudInitFile = nil
                    case .other:
                        selectCloudInitFile()
                    default:
                        break
                    }
                }
                Picker(selection: userDataBinding, label: Text("Cloud-init")) {
                    Text("None").tag(FileItem.none)
                    Divider()
                    if let cloudInitFile {
                        Text(cloudInitFile.lastPathComponent).tag(
                            FileItem.file(cloudInitFile))
                    }
                    Divider()
                    Text("Select User Data…").tag(FileItem.other)
                }
                .disabled(!distro.hasCloudVariant)

                TextField("Username", text: $defaultUsername, prompt: Text(Files.username))
            }

            CreateButtonRow {
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
                await vmModel.tryCreateContainer(
                    name: name, distro: distro, version: version, arch: arch,
                    cloudInitUserData: cloudInitFile,
                    defaultUsername: defaultUsername.isEmpty ? nil : defaultUsername)
            }
            isPresented = false
        }
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

            version = $0.versions.last!.key
        }
        .windowHolder(windowHolder)
    }

    private func selectCloudInitFile() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.allowedContentTypes = [UTType.yaml]
        panel.canChooseDirectories = false
        panel.canCreateDirectories = false
        panel.message = "Select user data file for Cloud-init setup"

        guard let window = windowHolder.window else { return }
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                cloudInitFile = url
            }
        }
    }

}
