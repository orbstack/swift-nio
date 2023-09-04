//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var actionTracker: ActionTracker

    @Default(.selectedTab) private var rootSelectedTab
    @State private var selection: String?
    @State private var creatingOpacity = 0.0

    var body: some View {
        StateWrapperView {
            if let containers = vmModel.containers {
                VStack {
                    if containers.contains(where: { !$0.builtin }) {
                        let filteredContainers = containers.filter { !$0.builtin }
                        // see DockerContainerItem for rowHeight calculation
                        AKFlatList(filteredContainers, selection: $selection, rowHeight: 48) { container in
                            MachineContainerItem(record: container)
                            .environmentObject(vmModel)
                            .environmentObject(actionTracker)
                        }
                    } else {
                        Spacer()
                        HStack {
                            Spacer()
                            VStack {
                                ContentUnavailableViewCompat("No Linux machines", systemImage: "desktopcomputer")

                                Button(action: {
                                    vmModel.presentCreateMachine = true
                                }) {
                                    Text("New Machine")
                                        .padding(6)
                                }
                                .controlSize(.large)
                                .keyboardShortcut(.defaultAction)
                            }
                            Spacer()
                        }
                        Spacer()

                        HStack {
                            Spacer()
                            VStack(spacing: 8) {
                                Text("Looking for Docker?")
                                        .font(.title3)
                                        .bold()
                                Text("You don’t need a Linux machine.")
                                        .font(.body)
                                        .padding(.bottom, 8)
                                Button(action: {
                                    rootSelectedTab = "docker"
                                }) {
                                    Text("Go to Containers")
                                }
                                .controlSize(.large)
                            }
                            .padding(.vertical, 24)
                            .padding(.horizontal, 48)
                            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                            Spacer()
                        }
                        .padding(.bottom, 48)
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
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Machines")
    }
}
