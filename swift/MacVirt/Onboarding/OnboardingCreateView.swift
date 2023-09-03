//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct OnboardingCreateView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    @EnvironmentObject private var vmModel: VmViewModel
    let onboardingController: OnboardingController

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

    var body: some View {
        VStack {
            Text("Create a Linux machine")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("This is a Linux machine that integrates with macOS. You can use it to build code, run services, and more.")
                .multilineTextAlignment(.center)
                .font(.title3)
                .foregroundColor(.secondary)
                .padding(.bottom, 8)
                .frame(maxWidth: 450)

            Spacer()

            HStack {
                Spacer()
                VStack(alignment: .center) {
                    let nameBinding = Binding<String>(get: { name }, set: {
                        if $0 != name {
                            self.nameChanged = true
                        }
                        self.name = $0
                    })

                    Form {
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

                        Picker("Distribution", selection: $distro) {
                            ForEach(Distro.allCases, id: \.self) { distro in
                                Text(distro.friendlyName).tag(distro)
                            }
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
                        .onChange(of: name) { newName in
                            checkName(newName)
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
                        .task {
                            checkName(name)
                        }
                        .onChange(of: vmModel.containers) { _ in
                            checkName(name)
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
                    }.frame(minWidth: 200)
                }.fixedSize()
                Spacer()
            }

            Spacer()

            HStack(alignment: .bottom) {
                Button(action: {
                    onboardingModel.back()
                }) {
                    Text("Back")
                }
                .buttonStyle(.borderless)
                Spacer()
                CtaButton(label: "Create", action: {
                    create()
                })
                // empty is disabled but not error
                .disabled(isNameDuplicate || isNameInvalid || name.isEmpty)
                Spacer()
            }
        }
    }

    private func checkName(_ newName: String) {
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
    }

    private func create() {
        // disabled
        if isNameDuplicate || isNameInvalid || name.isEmpty {
            return
        }

        Task { @MainActor in
            // wait for scon before doing anything - might not be started yet during onboarding
            await vmModel.waitForStateEquals(.running)

            // user picked linux, so stop docker container to save memory
            if let machines = vmModel.containers,
               let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }) {
                await vmModel.tryStopContainer(dockerRecord)
            }

            // then create
            await vmModel.tryCreateContainer(name: name, distro: distro, version: version, arch: arch)
        }
        onboardingController.finish()
    }
}
