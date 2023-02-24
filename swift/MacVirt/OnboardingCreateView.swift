//
// Created by Danny Lin on 2/6/23.
//

import Foundation
import SwiftUI

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
                        Picker("Distribution", selection: $distro) {
                            ForEach(Distro.allCases, id: \.self) { distro in
                                Text(distro.friendlyName).tag(distro)
                            }
                        }
                        .onChange(of: distro) {
                            if !nameChanged {
                                name = $0.rawValue
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
                            }
                        }
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
                Spacer()
            }
        }
    }
}
