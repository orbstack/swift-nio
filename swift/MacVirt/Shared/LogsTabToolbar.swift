import SwiftUI

 struct LogsTabToolbarWrapper<Content: View>: View {
    @StateObject private var commandModel = CommandViewModel()

    @ViewBuilder let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        VStack(spacing: 0) {
            // TODO: fix safeAreaInset
            VStack(spacing: 0) {
                HStack(alignment: .center) {
                    Spacer()

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

                    Button {
                        commandModel.searchCommand.send()
                    } label: {
                        Image(systemName: "magnifyingglass")
                    }
                    .help("Search")
                    .buttonStyle(.accessoryBar)
                }
                .padding(.horizontal, 12)
                .frame(height: 27) // match list section header height. wtf is this number?

                Divider()
            }
            .background(Color(NSColor.secondarySystemFill))

            content()
            .environmentObject(commandModel)
        }
    }
}
