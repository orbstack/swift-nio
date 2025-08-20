import SwiftUI

private struct AKSearchField: NSViewRepresentable {
    @Binding var text: String

    func makeNSView(context: Context) -> NSSearchField {
        let searchField = NSSearchField()
        searchField.delegate = context.coordinator
        return searchField
    }

    func updateNSView(_ nsView: NSSearchField, context: Context) {
        context.coordinator.binding = $text
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(binding: $text)
    }

    class Coordinator: NSObject, NSSearchFieldDelegate {
        var binding: Binding<String>

        init(binding: Binding<String>) {
            self.binding = binding
        }

        func controlTextDidChange(_ obj: Notification) {
            guard let searchField = obj.object as? NSSearchField else { return }
            binding.wrappedValue = searchField.stringValue
        }
    }
}

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
                        AKSearchField(text: $commandModel.searchField)

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
                    .frame(height: 28)  // match list section header height. wtf is this number?
                }
                .background(.ultraThickMaterial)
            }
            .logsTopInset(28)
            .environmentObject(commandModel)
    }
}
