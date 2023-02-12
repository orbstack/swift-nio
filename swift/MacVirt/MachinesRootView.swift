//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?
    @State private var presentCreate = false
    @State private var creatingOpacity = 0.0

    var body: some View {
        StateWrapperView {
            if let containers = vmModel.containers {
                List(selection: $selection) {
                    ForEach(containers) { container in
                        if !container.builtin {
                            MachineContainerItem(record: container)
                        }
                    }

                    if !containers.contains(where: { !$0.builtin }) {
                        HStack {
                            Spacer()
                            VStack {
                                Text("No Linux machines")
                                        .font(.title)
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
                        .overlay(alignment: .bottomTrailing, content: {
                            HStack {
                                Text("Creating")
                                ProgressView()
                                        .scaleEffect(0.5)
                                        .frame(width: 16, height: 16)
                            }
                            .padding(8)
                            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                            .opacity(creatingOpacity)
                            .padding(16)
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
                        .onChange(of: vmModel.creatingCount) { newValue in
                            if newValue > 0 {
                                withAnimation {
                                    creatingOpacity = 1
                                }
                            } else {
                                withAnimation {
                                    creatingOpacity = 0
                                }
                            }
                        }
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Machines")
    }
}
