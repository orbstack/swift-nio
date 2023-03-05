//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct FilesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?

    var body: some View {
        StateWrapperView {
            if let containers = vmModel.containers {
                List(selection: $selection) {
                    ForEach(containers) { container in
                        FileContainerItem(record: container)
                    }

                    HStack {
                        Spacer()
                        VStack {
                            Text("You can also find these files in ~/Linux.")
                                    .font(.title3)
                                    .foregroundColor(.secondary)
                        }
                                .padding(.vertical, 24)
                        Spacer()
                    }
                }
                .refreshable {
                    NSLog("try refresh: files refreshable")
                    await vmModel.tryRefreshList()
                }
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Files")
    }
}
