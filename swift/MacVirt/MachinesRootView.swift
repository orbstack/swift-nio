//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?
    @State private var presentCreate = false
    @State private var isCreating = false

    var body: some View {
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
                            .frame(height: 1, alignment: .center)

                    HStack {
                        Text("Creating...")
                        Spacer()
                    }
                }
                .background()
                .opacity(isCreating ? 1 : 0)
                .animation(.easeInOut)
            })
            .toolbar {
                Button(action: {
                    presentCreate = true
                }) {
                    Label("New Machine", systemImage: "plus")
                }.popover(isPresented: $presentCreate) {
                            CreateContainerView(isPresented: $presentCreate, isCreating: $isCreating)
                        }
            }
            .navigationTitle("Machines")
        } else {
            ProgressView(label: {
                Text("Loading")
            })
        }
    }
}