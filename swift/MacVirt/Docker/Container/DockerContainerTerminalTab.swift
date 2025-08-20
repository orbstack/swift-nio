import SwiftUI

struct DockerContainerTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var useDebugShell: Bool = true

    let container: DKContainer

    private var statusBar: some View {
        VStack(spacing: 0) {
            HStack(alignment: .center) {
                Spacer()
                Toggle("Debug Shell", isOn: Binding(
                    get: { useDebugShell && vmModel.isLicensed },
                    set: { useDebugShell = $0 }
                ))
                    .toggleStyle(.checkbox)
                    .font(
                        .system(
                            size: 13,
                            weight: .regular)
                    )
                .disabled(!vmModel.isLicensed)
            }
            .padding(.horizontal, 16)
            .frame(height: 27) // match list section header height. wtf is this number?

            Divider()
        }
        .background(Color(NSColor.secondarySystemFill))
    }

    private var terminal: some View {
        TerminalView(
            command: (useDebugShell || !vmModel.isLicensed)  // if not licensed, use debug shell so we get the ad at top
                ? [AppConfig.ctlExe, "debug", "-f", container.id]
                : [AppConfig.dockerExe, "exec", "-it", container.id, "sh", "-c", "command -v bash > /dev/null && exec bash || exec sh"],
            env: [
                // env is more robust, user can mess with context
                "DOCKER_HOST=unix://\(Files.dockerSocket)",
                // don't show docker debug ads
                "DOCKER_CLI_HINTS=0",
            ]
        )
    }

    private var contentUnavailable: some View {
        ZStack(alignment: .center) {
            ContentUnavailableViewCompat("Container Not Running", systemImage: "moon.zzz.fill") {
                Button {
                    Task {
                        await vmModel.tryDockerContainerStart(container.id)
                    }
                } label: {
                    Text("Start")
                        .padding(.horizontal, 4)
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .controlSize(.large)
            }

            VStack {
                Spacer()
                HStack(spacing: 0) {
                    Text("Debug stopped containers with ")
                        .font(.system(size: 14, weight: .regular))
                        .foregroundColor(.secondary)
                    Text("Debug Shell")
                        .font(.system(size: 14, weight: .regular))
                        .foregroundColor(.accentColor)
                        .underline()
                        .cursorRect(NSCursor.pointingHand)
                        .onTapGesture {
                            NSWorkspace.shared.open(URL(string: "https://orb.cx/debug")!)
                        }
                    Text(".")
                        .font(.system(size: 14, weight: .regular))
                        .foregroundColor(.secondary)
                }
            }.padding(.bottom, 32)
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if (useDebugShell && vmModel.isLicensed) || container.running {
                statusBar
                terminal
            } else {
                ZStack(alignment: .top) {
                    statusBar
                    contentUnavailable
                }
            }
        }
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenContainerInNewWindow {
                if useDebugShell || !vmModel.isLicensed {
                    container.openDebugShellFallback()
                } else {
                    container.openInPlainTerminal()
                }
            }
        }
    }
}
