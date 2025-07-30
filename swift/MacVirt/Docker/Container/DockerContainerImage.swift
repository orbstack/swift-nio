import SwiftUI

struct DockerContainerImage: View {
    let container: DKContainer

    var body: some View {
        // 28px
        let color = SystemColors.forString(container.userName)
        Image(systemName: "shippingbox.fill")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 16, height: 16)
            .padding(6)
            .foregroundColor(Color(hex: 0xFAFAFA))
            .background(Circle().fill(color))
            // rasterize so opacity works on it as one big image
            .drawingGroup(opaque: false)
    }
}
