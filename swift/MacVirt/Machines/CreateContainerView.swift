//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

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
    @State private var isNameDuplicate = false
    @State private var isNameInvalid = false
    @State private var duplicateHeight = 0.0
    #if arch(arm64)
    @State private var arch = "arm64"
    #else
    @State private var arch = "amd64"
    #endif
    @State private var distro = Distro.ubuntu
    @State private var version = Distro.ubuntu.versions.last!.key
    @State private var cloudInitFile: URL? = nil

    @Binding var isPresented: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("New Machine")
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

                    Spacer()
                    .frame(height: 20)

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
                    Picker("CPU type", selection: $arch) {
                        Text("Apple").tag("arm64")
                        Text("Intel").tag("amd64")
                    }
                            .pickerStyle(.segmented)
                            .disabled(distro == .nixos)
                    #endif

                    Spacer()
                        .frame(height: 20)

                    DisclosureGroup("Advanced") {
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
                                Text(cloudInitFile.lastPathComponent).tag(FileItem.file(cloudInitFile))
                            }
                            Divider()
                            Text("Select User Data…").tag(FileItem.other)
                        }
                        .disabled(!distro.hasCloudVariant)
                    }
                    .frame(maxWidth: .infinity, maxHeight: 24, alignment: .leading)
                    .padding(.bottom, 20)
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
                    Text("Create")
                }
                .keyboardShortcut(.defaultAction)
                // empty is disabled but not error
                .disabled(isNameDuplicate || isNameInvalid || name.isEmpty)
            }
            .padding(.top, 8)
        }
        .padding(20)
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
        .onChange(of: name) { newName in
            checkName(newName)
        }
        .onAppear {
            checkName(name, animate: false)
        }
        .onChange(of: vmModel.containers) { _ in
            checkName(name)
        }
        .background(WindowAccessor(holder: windowHolder))
    }

    private func selectCloudInitFile() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.canCreateDirectories = false

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
               let url = panel.url {
                cloudInitFile = url
            }
        }
    }

    private func checkName(_ newName: String, animate: Bool = true) {
        if let containers = vmModel.containers,
           containers.contains(where: { $0.name == newName }) {
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
            await vmModel.tryCreateContainer(name: name, distro: distro, version: version, arch: arch,
                    cloudInitUserData: cloudInitFile)
        }
        isPresented = false
    }
}