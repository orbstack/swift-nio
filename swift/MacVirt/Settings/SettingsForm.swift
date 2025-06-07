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
        // -20 top padding makes it touch, but macOS Settings > Appearance > Show scroll bars > "Automatically based on mouse or trackpad" makes the border and/or lighter-colored toolbar show up and overlap it unless splitViewItem.titlebarSeparatorStyle=.none, which is also not what we want
        .formStyle(.grouped)
    }
}
