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
    @Binding var isCreating: Bool

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
                Picker("CPU type", selection: $arch) {
                    #if arch(arm64)
                    Text("Apple").tag("arm64")
                    Text("Intel").tag("amd64")
                    #else
                    Text("64-bit").tag("amd64")
                    Text("32-bit").tag("i386")
                    #endif
                }

                Button(action: {
                    Task {
                        isCreating = true
                        do {
                            try await self.vmModel.createContainer(name: name, distro: distro, arch: arch)
                        } catch {
                            print("create err", error)
                        }
                        isCreating = false
                    }
                    isPresented = false
                }) {
                    Text("Create")
                }
            }
        }
        .navigationTitle("New Machine")
        .padding(16)
        .onChange(of: distro) {
            if !nameChanged {
                name = $0.rawValue
            }
        }
    }
}