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
                let haveContainers = containers.contains(where: { !$0.builtin })

                VStack {
                    if haveContainers {
                        List(selection: $selection) {
                            ForEach(containers) { container in
                                if !container.builtin {
                                    FileContainerItem(record: container)
                                }
                            }

                            HStack {
                                Spacer()
                                VStack {
                                    Text("You can also find these files at ~/\(Folders.nfsName).")
                                            .font(.title3)
                                            .foregroundColor(.secondary)
                                }
                                        .padding(.vertical, 24)
                                Spacer()
                            }
                        }
                    } else {
                        Spacer()

                        HStack {
                            Spacer()
                            VStack {
                                Text("No Linux machines")
                                        .font(.title)
                                        .foregroundColor(.secondary)
                            }
                                    .padding(.top, 32)
                            Spacer()
                        }

                        Spacer()
                    }
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
