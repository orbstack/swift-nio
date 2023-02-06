//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?
    @State private var presentCreate = false

    var body: some View {
        StateWrapperView {
            if let containers = vmModel.containers {
                List(selection: $selection) {
                    ForEach(containers) { container in
                        if !container.builtin {
                            ContainerItem(record: container)
                        }
                    }

                    if containers.isEmpty {
                        HStack {
                            Spacer()
                            VStack {
                                Text("No Linux machines")
                                        .font(.largeTitle)
                                        .foregroundColor(.secondary)
                                Button(action: {
                                    presentCreate = true
                                }) {
                                    Text("New Machine")
                                }
                            }
                                    .padding(.top, 32)
                            Spacer()
                        }
                    }
                }
                        .refreshable {
                            await vmModel.tryRefreshList()
                        }
                        .overlay(alignment: .bottom, content: {
                            VStack {
                                ProgressView()
                                        .progressViewStyle(.linear)
                                Text("Creating...")
                            }
                                    .padding(.vertical, 8)
                                    .padding(.horizontal, 32)
                                    .background(.thinMaterial)
                                    .opacity(vmModel.creatingCount > 0 ? 1 : 0)
                                    .animation(.spring())
                        })
                        .toolbar {
                            Button(action: {
                                presentCreate = true
                            }) {
                                Label("New Machine", systemImage: "plus")
                            }.popover(isPresented: $presentCreate) {
                                        CreateContainerView(isPresented: $presentCreate, creatingCount: $vmModel.creatingCount)
                                    }
                                    .help("New machine")
                        }
                        .navigationTitle("Machines")
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
                        .navigationTitle("Machines")
            }
        }
    }
}