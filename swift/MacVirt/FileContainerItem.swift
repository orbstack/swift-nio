//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct FileContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    var body: some View {
        HStack {
            let color = SystemColors.forString(record.id)
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