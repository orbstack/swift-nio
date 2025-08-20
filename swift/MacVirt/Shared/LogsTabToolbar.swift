import SwiftUI

struct LogsTabToolbarWrapper<Content: View>: View {
    @StateObject private var commandModel = CommandViewModel()

    @ViewBuilder let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        content()
        .safeAreaInset(edge: .top) {
            VStack(spacing: 0) {
                HStack(alignment: .center) {
                    TextField(text: $commandModel.searchField) {
                        Label("Search", systemImage: "magnifyingglass")
                    }
                    .textFieldStyle(.roundedBorder)

                    Button {
                        commandModel.copyAllCommand.send()
                    } label: {
                        Image(systemName: "doc.on.doc")
                    }
                    .help("Copy")
                    .keyboardShortcut("c", modifiers: [.command, .shift])
                    .buttonStyle(.accessoryBar)

                    Button {
                        commandModel.clearCommand.send()
                    } label: {
                        Image(systemName: "trash")
                    }
                    .help("Clear")
                    .keyboardShortcut("k", modifiers: [.command])
                    .buttonStyle(.accessoryBar)
                }
                .padding(.horizontal, 12)
                .frame(height: 28) // match list section header height. wtf is this number?
            }
            .background(.ultraThickMaterial)
        }
        .logsTopInset(28)
        .environmentObject(commandModel)
    }
}
