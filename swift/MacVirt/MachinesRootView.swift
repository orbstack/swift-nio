//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @AppStorage("root.selectedTab") private var rootSelectedTab = "docker"
    @State private var selection: String?
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
                                    vmModel.presentCreate = true
                                }) {
                                    Text("New Machine")
                                }

                                Spacer().frame(height: 64)

                                VStack(spacing: 8) {
                                    Text("Looking for Docker?")
                                            .font(.title3)
                                            .bold()
                                    Text("You can use Docker directly from macOS.")
                                            .font(.body)
                                            .padding(.bottom, 8)
                                    Button(action: {
                                        rootSelectedTab = "docker"
                                    }) {
                                        Text("Go to Docker")
                                    }
                                }
                                .padding(16)
                                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
                            }
                            .padding(.top, 32)
                            Spacer()
                        }
                    }
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
