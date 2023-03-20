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

struct FileContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    var body: some View {
        HStack {
            let color = colors[record.id.hashValue %% colors.count]
            Image(systemName: "folder.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
                    .foregroundColor(color)

            VStack(alignment: .leading) {
                Text(record.name)
                        .font(.body)
            }
            Spacer()

            Button(action: {
                openOne(record)
            }) {
                Image(systemName: "folder.fill")
            }
            .buttonStyle(.borderless)
            .help("Show machine files")
        }
        .padding(.vertical, 4)
        .onDoubleClick {
            openOne(record)
        }
        .contextMenu {
            Button(action: {
                openOne(record)
            }) {
                Label("Open", systemImage: "folder")
            }
        }
    }

    func openOne(_ container: ContainerRecord) {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfs)/\(container.name)")
    }
}