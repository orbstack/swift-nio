import SwiftUI

struct SettingsForm<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        Form {
            content()
        }
        // safe area is a bit too big and includes scene padding
        .padding(.top, -20)
        .formStyle(.grouped)
    }
}
