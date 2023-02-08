//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

fileprivate let colors = [
    Color(.systemRed),
    Color(.systemGreen),
    Color(.systemBlue),
    Color(.systemOrange),
    Color(.systemYellow),
    Color(.systemBrown),
    Color(.systemPink),
    Color(.systemPurple),
    Color(.systemGray),
    Color(.systemTeal),
    Color(.systemIndigo),
    Color(.systemMint),
    Color(.systemCyan),
]

struct DockerContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var container: DockerContainer

    var body: some View {
        HStack {
            let color = colors[container.id.hashValue %% colors.count]
            Image(systemName: "shippingbox.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
                    .foregroundColor(color)

            VStack(alignment: .leading) {
                let nameTxt = container.names
                        .map { $0.deletingPrefix("/") }
                        .joined(separator: ", ")
                let name = nameTxt.isEmpty ? "(no name)" : nameTxt
                Text(name)
                        .font(.body)

                let shortId = String(container.id.prefix(12))
                Text("\(shortId) (\(container.image))")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
            }
            Spacer()
        }
        .padding(.vertical, 4)
        .onDoubleClick {
            Task {
                do {
                    try await openTerminal(AppConfig.c.dockerExe, ["exec", "-it", container.id, "sh"])
                } catch {
                    print(error)
                }
            }
        }
    }
}