//
// Created by Danny Lin on 2/6/23.
//

import Combine
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

    @State private var createButtonPressed = PassthroughSubject<Void, Never>()

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
                    CreateForm(submitCommand: createButtonPressed) {
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
                        }

                        Section {
                            Picker("Distribution", selection: $distro) {
                                ForEach(Distro.allCases, id: \.self) { distro in
                                    Text(distro.friendlyName).tag(distro)
                                }
                            }
                            .onChange(of: distro) { _, distro in
                                if !nameChanged {
                                    name = distro.rawValue
                                }

                                #if arch(arm64)
                                    // NixOS doesn't work with Rosetta
                                    if distro == .nixos {
                                        arch = "arm64"
                                    }
                                #endif

                                version = distro.versions.last!.key
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
                                    Text("x86-64 (emulated)").tag("amd64")
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
                    }
                    .scrollContentBackground(.hidden)
                    .scrollDisabled(true)  // should never overflow due to fixed-size onboarding window
                    .frame(maxWidth: 400)
                }.fixedSize()
                Spacer()
            }

            Spacer()

            Grid(alignment: .center) {
                GridRow {
                    HStack(alignment: .bottom) {
                        Button {
                            onboardingModel.back()
                        } label: {
                            Image(systemName: "chevron.left")
                                .resizable()
                                .foregroundColor(.secondary)
                                .aspectRatio(contentMode: .fit)
                                .frame(height: 16)
                                .padding(6)
                        }
                        .buttonStyle(.borderless)

                        Spacer()
                    }

                    CtaButton("Create") {
                        createButtonPressed.send()
                    }

                    Color.clear
                        .gridCellUnsizedAxes(.vertical)
                }
            }
        }
    }
}
