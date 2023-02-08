//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

fileprivate struct CommandSection<Content: View>: View {
    let systemImage: String?
    let title: String?
    let desc: String?
    let content: () -> Content

    init(systemImage: String? = nil, title: String? = nil, desc: String? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.systemImage = systemImage
        self.title = title
        self.desc = desc
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                if let systemImage {
                    Image(systemName: systemImage)
                }

                if let title {
                Text(title)
                    .font(.title2)
                    .bold()
                }
            }
            if let desc {
                Text(desc)
                    .font(.body)
                    .foregroundColor(.secondary)
            }

            content()
        }.frame(maxWidth: .infinity)
                .padding(8)
    }
}

fileprivate struct CommandBox: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let title: String
    let desc: String?
    let command: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                    .font(.title3)
                    .bold()
            if let desc {
                Text(desc)
                    .font(.body)
                    .foregroundColor(.secondary)
            }
            Text(command)
                .font(.body.monospaced())
                .padding(4)
                .background(.thickMaterial, in: RoundedRectangle(cornerRadius: 4))
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
        }
    }
}

struct CommandsRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    var body: some View {
        ScrollView {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 12) {
                    CommandSection(systemImage: "info.circle", title: "Get started") {
                        CommandBox(
                            title: "Control machines: moonctl",
                            desc: "Create, start, stop, delete, and more",
                            command: "moonctl help"
                        )

                        CommandBox(
                            title: "Short command: moon",
                            desc: "This can be used to start a shell, run commands, or control machines.",
                            command: "moon help"
                        )
                    }

                    CommandSection(systemImage: "terminal", title: "Command line") {
                        CommandBox(
                            title: "Start a shell",
                            desc: "By default, the last-used machine is used.",
                            command: "moon"
                        )

                        CommandBox(
                            title: "Start a shell in a specific machine",
                            desc: "By default, the last-used machine is used.",
                            command: "moon -m ubuntu"
                        )
                    }

                    CommandSection(systemImage: "network", title: "SSH", desc: "SSH access is also available. You can use this with apps like Visual Studio Code and JetBrains IDEs.") {
                        CommandBox(
                            title: "Log in",
                            desc: "Run a command or log in to the last-used machine.",
                            command: "ssh macvirt"
                        )

                        CommandBox(
                            title: "Specify machine and user",
                            desc: "Run a command or log in to a specific machine, as a specific user.",
                            command: "ssh root@ubuntu@macvirt"
                        )

                        CommandBox(
                            title: "Connection details for other apps",
                            desc: "For apps that don’t use OpenSSH, you can use the following details.",
                            command: """
                                     Host: localhost
                                     Port: 62222
                                     User: default (or root@ubuntu)
                                     Private key: ~/.macvirt/ssh/id_ed25519
                                     """
                        )
                    }

                    Spacer()
                }
                Spacer()
            }
            .padding()
        }
        .navigationTitle("Commands")
    }
}

struct CommandsRootView_Previews: PreviewProvider {
    static var previews: some View {
        CommandsRootView()
    }
}
