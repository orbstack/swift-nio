//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern =
    (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct CreateNetworkView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var name = ""
    @State private var subnet = ""
    @State private var enableIPv6 = false

    @Binding var isPresented: Bool

    var body: some View {
        CreateForm {
            Section {
                let nameBinding = Binding<String>(
                    get: { name },
                    set: {
                        self.name = $0
                    })

                ValidatedTextField(
                    "Name", text: nameBinding,
                    validate: { value in
                        // duplicate
                        if vmModel.dockerNetworks?[value] != nil {
                            return "Already exists"
                        }

                        // regex
                        if dockerRestrictedNamePattern.firstMatch(
                            in: value, options: [],
                            range: NSRange(location: 0, length: value.utf16.count))
                            == nil
                        {
                            return "Invalid name"
                        }

                        return nil
                    })
            } header: {
                Text("New Network")
                Text(
                    "Bridge networks are groups of containers in the same subnet (IP range) that can communicate with each other. They are typically used by Compose, and don’t need to be manually created or deleted."
                )
            }

            Section("Advanced") {
                Toggle("IPv6", isOn: $enableIPv6)

                TextField("Subnet (IPv4)", text: $subnet, prompt: Text("172.30.30.0/24"))
            }

            CreateButtonRow {
                HelpButton {
                    NSWorkspace.shared.open(
                        URL(string: "https://orb.cx/docker-docs/network-create")!)
                }

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
                await vmModel.tryDockerNetworkCreate(
                    name, subnet: subnet.isEmpty ? nil : subnet, enableIPv6: enableIPv6)
            }
            isPresented = false
        }.onAppear {
            enableIPv6 = vmModel.dockerEnableIPv6
        }
    }
}
