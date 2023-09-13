//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

private struct CommandSection<Content: View>: View {
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
        VStack(alignment: .leading) {
            HStack(alignment: .center) {
                if let systemImage {
                    Image(systemName: systemImage)
                }

                if let title {
                    Text(title)
                        .font(.title2)
                        .bold()
                            // wrap, don't ellipsize
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if let desc {
                Text(desc)
                    .font(.title3)
                    .foregroundColor(.secondary)
                        // wrap, don't ellipsize
                    .fixedSize(horizontal: false, vertical: true)
            }

            content()
                .padding(.top, 4)
        }.frame(maxWidth: .infinity)
        .padding(8)
    }
}

private struct CommandBox: View {
    @EnvironmentObject private var vmModel: VmViewModel

    private let title: String
    private let desc: String?
    private let command: String
    private let selectable: Bool

    init(title: String, desc: String? = nil, command: String, selectable: Bool = false) {
        self.title = title
        self.desc = desc
        self.command = command
        self.selectable = selectable
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                    .font(.title3)
                    .bold()
                    // wrap, don't ellipsize
                    .fixedSize(horizontal: false, vertical: true)
            if let desc {
                Text(try! AttributedString(markdown: desc))
                    .font(.body)
                    .foregroundColor(.secondary)
                    // wrap, don't ellipsize
                    .fixedSize(horizontal: false, vertical: true)
            }

            Group {
                if selectable {
                    Text(command)
                    .textSelectionWithWorkaround()
                    .font(.body.monospaced())
                    .padding(4)
                    .background(.thickMaterial, in: RoundedRectangle(cornerRadius: 4))
                } else {
                    CopyableText(copyAs: command) {
                        Text(command)
                        .font(.body.monospaced())
                        .padding(4)
                        .background(.thickMaterial, in: RoundedRectangle(cornerRadius: 4))
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct CommandsRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    var body: some View {
        ScrollView {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 32) {
                    CommandSection(systemImage: "info.circle.fill", title: "Get started") {
                        CommandBox(
                            title: "All-purpose command: orb",
                            desc: "Manage OrbStack and its machines, start a shell, or run a Linux command.",
                            command: "orb"
                        )
                    }

                    CommandSection(systemImage: "shippingbox.fill", title: "Docker", desc: "Use the included Docker commands directly from macOS. No Linux machines needed.") {
                        CommandBox(
                            title: "Main command",
                            desc: "Build and run containers, and more.",
                            command: "docker help"
                        )

                        CommandBox(
                            title: "Compose",
                            desc: "Build and run multiple containers at once.",
                            command: "docker compose"
                        )

                        CommandBox(
                            title: "Run a container",
                            desc: "Start an example server and access it at [localhost](http://localhost/).",
                            command: "docker run -it -p 80:80 docker/getting-started"
                        )
                    }

                    CommandSection(systemImage: "terminal.fill", title: "Command line", desc: "Environment variables and SSH agent are forwarded by default.") {
                        CommandBox(
                                title: "Start a shell",
                                desc: "Log in as the default user in the machine you used most recently.",
                                command: "orb"
                        )

                        CommandBox(
                                title: "Log in as a specific user and machine",
                                desc: "Use the same flags as “orb shell”.",
                                command: "orb -m ubuntu -u root"
                        )

                        CommandBox(
                                title: "Run a command",
                                desc: "Prefix any command with “orb” to run it in a Linux machine.",
                                command: "orb uname -a"
                        )
                    }

                    let sshConfigMsg = vmModel.isSshConfigWritable ? "" : "\nSee “orb ssh” for instructions to add OrbStack to your SSH config."
                    CommandSection(systemImage: "network", title: "SSH", desc: "SSH is also supported. You can use this with apps like Visual Studio Code.\(sshConfigMsg)") {
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
                                         Port: 32222
                                         User: default (or root@ubuntu)
                                         Private key: ~/.orbstack/ssh/id_ed25519
                                         """,
                                selectable: true
                        )
                    }

                    CommandSection(systemImage: "macwindow", title: "Linux → macOS") {
                        CommandBox(
                                title: "Start a Mac shell",
                                desc: "Start a shell from Linux.",
                                command: "mac"
                        )

                        CommandBox(
                                title: "Run a Mac command",
                                desc: "Run a command from Linux.",
                                command: "mac uname -a"
                        )
                    }

                    CommandSection(systemImage: "folder.fill", title: "File transfer", desc: "You can also use shared folders at /Users and ~/\(Folders.nfsName) to transfer files.") {
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
        .task {
            await vmModel.tryRefreshSshConfigWritable()
        }
    }
}

struct CommandsRootView_Previews: PreviewProvider {
    static var previews: some View {
        CommandsRootView()
    }
}
