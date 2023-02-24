//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct CreateContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = "ubuntu"
    @State private var nameChanged = false
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
            }
        }
        .padding(16)
        .onChange(of: distro) {
            if !nameChanged {
                name = $0.rawValue
            }
        }
    }
}