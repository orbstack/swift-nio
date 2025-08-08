import SwiftUI

struct DockerContainerTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var useDebugShell = true
    @State private var title = ""

    let container: DKContainer

    private var statusBar: some View {
        HStack(alignment: .center) {
            Text(title)
                .font(.system(size: 13, weight: .semibold))
                .padding(.vertical, 4)
                .padding(.horizontal, 6)
                .onAppear {
                    title = ""
                }
                .onReceive(NotificationCenter.default.publisher(for: .ghosttySetTitle)) {
                    notification in
                    if let newTitle = notification.object as? String {
                        title = newTitle
                    }
                }
            Spacer()

            Toggle("Debug Shell", isOn: $useDebugShell)
                .toggleStyle(.checkbox)
                .font(
                    .system(
                        size: 13,
                        weight: (!container.running && (!useDebugShell || !vmModel.isLicensed))
                            ? .semibold : .regular)
                )
                .padding(.vertical, 4)
                .padding(.horizontal, 6)
        }
        .padding(.all, 4)
    }

    private var terminal: some View {
        TerminalView(
            command: (useDebugShell || !vmModel.isLicensed)  // if not licensed, use debug shell so we get the ad at top
                ? AppConfig.ctlExe + " debug -f \(container.id)"
                : AppConfig.dockerExe
                    + " exec -it \(container.id) sh -c 'command -v bash > /dev/null && exec bash || exec sh'",
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
            VStack(spacing: 16) {  // match ContentUnavailableViewCompat desc padding
                ContentUnavailableViewCompat(
                    "Container Not Running", systemImage: "moon.zzz.fill")

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
            if vmModel.isLicensed {
                if useDebugShell || container.running {
                    statusBar
                    terminal
                } else {
                    ZStack(alignment: .top) {
                        statusBar
                        contentUnavailable
                    }
                }
            } else if container.running {
                terminal
            } else {
                contentUnavailable
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
