import SwiftUI

struct SettingsFooter<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        Section {
        } footer: {
            HStack {
                content()
            }
            .frame(maxWidth: .infinity, alignment: .trailing)
            .padding(.top, 12)
        }
    }
}
