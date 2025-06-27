//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let containerNameRegex = try! NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9-.]+$")
// .orb.internal domains, plus "default" special ssh name
private let containerNameBlacklist = ["default", "vm", "host", "services", "gateway"]

struct OnboardingCreateView: View {
    @EnvironmentObject private var onboardingModel: OnboardingViewModel
    @EnvironmentObject private var vmModel: VmViewModel
    let onboardingController: OnboardingController

    @State private var name = "ubuntu"
    @State private var nameChanged = false
    #if arch(arm64)
        @State private var arch = "arm64"
    #else
        @State private var arch = "amd64"
    #endif
    @State private var distro = Distro.ubuntu
    @State private var version = Distro.ubuntu.versions.last!.key

    @Environment(\.createSubmitFunc) private var submitFunc

    var body: some View {
        VStack {
            Text("Create a Linux machine")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text(
                "This is a Linux machine that integrates with macOS. You can use it to build code, run services, and more."
            )
            .multilineTextAlignment(.center)
            .font(.title3)
            .foregroundColor(.secondary)
            .padding(.bottom, 8)
            .frame(maxWidth: 450)

            Spacer()

            HStack {
                Spacer()
                VStack(alignment: .center) {
                    CreateForm {
                        Section {
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
                                    // empty is not allowed
                                    if value.isEmpty {
                                        return "Name cannot be empty"
                                    }

                                    // duplicate
                                    if let containers = vmModel.machines,
                                        containers.values.contains(where: {
                                            $0.record.name == value
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

                            Picker("Version", selection: $version) {
                                ForEach(distro.versions, id: \.self) { version in
                                    if version == distro.versions.last! && distro.versions.count > 1
                                    {
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
                    } onSubmit: {
                        Task { @MainActor in
                            // wait for scon before doing anything - might not be started yet during onboarding
                            await vmModel.waitForStateEquals(.running)

                            // user picked linux, so stop docker container to save memory
                            if let dockerMachine = vmModel.dockerMachine {
                                await vmModel.tryStopContainer(dockerMachine.record)
                            }

                            // then create
                            await vmModel.tryCreateContainer(
                                name: name, distro: distro, version: version, arch: arch)
                        }
                        onboardingController.finish()
                    }.frame(minWidth: 200)
                }.fixedSize()
                Spacer()
            }

            Spacer()

            HStack(alignment: .bottom) {
                Button {
                    onboardingModel.back()
                } label: {
                    Text("Back")
                }
                .buttonStyle(.borderless)
                Spacer()
                CtaButton(
                    label: "Create",
                    action: {
                        submitFunc()
                    }
                )
                Spacer()
            }
        }
    }
}
