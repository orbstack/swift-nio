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
            HStack(alignment: .center) {
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
                    CommandSection(systemImage: "info.circle.fill", title: "Get started") {
                        CommandBox(
                            title: "Control machines: orbctl",
                            desc: "Create, start, stop, delete, change default, and more. Explore the help for more commands.",
                            command: "orbctl help"
                        )

                        CommandBox(
                            title: "Short command: orb",
                            desc: "Start a shell, run commands, or control machines.",
                            command: "orb help"
                        )
                    }

                    CommandSection(systemImage: "terminal.fill", title: "Command line") {
                        CommandBox(
                            title: "Start a shell",
                            desc: "Log in as the default user in the machine you used most recently.",
                            command: "orb"
                        )

                        CommandBox(
                            title: "Start a shell as a specific user and machine",
                            desc: "Use the same flags as “orbctl shell”.",
                            command: "orb -m ubuntu -u root"
                        )

                        CommandBox(
                            title: "Run a command",
                            desc: "Prefix any command with “orb” to run it in a Linux machine.",
                            command: "orb uname -a"
                        )

                        CommandBox(
                            title: "Run a command as a specific user and machine",
                            desc: "The same flags can be used when prefixing a command with “orb”.",
                            command: "orb -m ubuntu -u root uname -a"
                        )
                    }

                    CommandSection(systemImage: "network", title: "SSH", desc: "SSH is also supported. You can use this with apps like Visual Studio Code and JetBrains IDEs.") {
                        CommandBox(
                            title: "Log in",
                            desc: "Run a command or log in to the default machine.",
                            command: "ssh orb"
                        )

                        CommandBox(
                            title: "Specify machine and user",
                            desc: "Run a command or log in as a specific user and machine.",
                            command: "ssh root@ubuntu@orb"
                        )

                        CommandBox(
                            title: "Connection details for other apps",
                            desc: "For apps that don’t use OpenSSH, you can use the following details.",
                            command: """
                                     Host: localhost
                                     Port: 62222
                                     User: default (or root@ubuntu)
                                     Private key: ~/.orbstack/ssh/id_ed25519
                                     """
                        )
                    }
                    
                    CommandSection(systemImage: "macwindow", title: "macOS from Linux") {
                        CommandBox(
                            title: "Start a Mac shell",
                            desc: "Start a shell on macOS from within Linux.",
                            command: "mac"
                        )

                        CommandBox(
                            title: "Run a Mac command",
                            desc: "Run a command on macOS from within Linux.",
                            command: "mac uname -a"
                        )
                    }
                    
                    CommandSection(systemImage: "folder.fill", title: "File transfer", desc: "We recommend transferring files via the /Users and ~/Linux shared folders, but commands are also available.") {
                        CommandBox(
                            title: "Copy files from Mac to Linux",
                            desc: "Push from Mac to the default Linux machine's home folder.",
                            command: "orb push example.txt"
                        )

                        CommandBox(
                            title: "Copy files from Linux to Mac",
                            desc: "Pull from the default Linux machine's home folder to Mac.",
                            command: "orb pull example.txt"
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
