//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

fileprivate let containerNamePattern = (try? NSRegularExpression(pattern: "^[a-zA-Z0-9_-]+$"))!

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

    var body: some View {
        VStack {
            Text("Create a Linux machine")
                .font(.largeTitle.weight(.semibold))
                .padding(.bottom, 4)
                .padding(.top, 16)
            Text("This is a full Linux machine that works like a VM, including running services with systemd or OpenRC.")
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
                    Task { @MainActor in
                        vmModel.creatingCount += 1
                        await vmModel.tryCreateContainer(name: name, distro: distro, arch: arch)
                        vmModel.creatingCount -= 1
                    }
                    onboardingController.finish()
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
        if !newName.isEmpty && containerNamePattern.firstMatch(in: newName, options: [], range: NSRange(location: 0, length: newName.utf16.count)) == nil {
            isNameInvalid = true
        } else {
            isNameInvalid = false
        }
    }
}
