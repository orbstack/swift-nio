//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: VmViewModel

    @State private var selection: String?
    @State private var startStopInProgress = false
    @State private var presentCreate = false
    @State private var isCreating = false

    var body: some View {
        let errorPresented = Binding<Bool>(get: {
            model.error != nil
        }, set: {
            if !$0 {
                model.error = nil
            }
        })

        Group {
            switch model.state {
            case .stopped:
                VStack {
                    Text("Not running")
                            .font(.largeTitle)
                    Button(action: {
                        Task {
                            await model.start()
                        }
                    }) {
                        Text("Start")
                    }
                    .buttonStyle(.borderedProminent)
                }
            case .spawning:
                ProgressView(label: {
                    Text("Updating")
                })
            case .starting:
                ProgressView(label: {
                    Text("Starting")
                })
            case .running:
                if let containers = model.containers {
                    List(selection: $selection) {
                        Section(header: Text("Features")) {
                            ForEach(containers) { container in
                                if container.builtin {
                                    BuiltinContainerItem(record: container)
                                }
                            }
                        }

                        Section(header: Text("Machines")) {
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
                    }
                    .refreshable {
                        await model.tryRefreshList()
                    }
                    .overlay(alignment: .bottom, content: {
                        VStack {
                            ProgressView()
                                    .progressViewStyle(.linear)
                                    .frame(height: 1, alignment: .center)
                                    .opacity(isCreating ? 1 : 0)
                                    .animation(.easeInOut)

                            HStack {
                                Text("Creating...")
                                        .opacity(isCreating ? 1 : 0)
                                        .animation(.easeInOut)
                                Spacer()
                            }
                        }
                        .background()
                    })
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            case .stopping:
                ProgressView(label: {
                    Text("Stopping")
                })
            }
        }
        .toolbar {
            Button(action: {
                Task {
                    self.startStopInProgress = true
                    if model.state == .running {
                        await model.stop()
                    } else {
                        await model.start()
                    }
                    self.startStopInProgress = false
                }
            }) {
                Label(model.state == .running ? "Stop" : "Start", systemImage: "power")
            }
                    .disabled(startStopInProgress)

            Button(action: {
                if #available(macOS 13, *) {
                    NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
                } else {
                    NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
                }
            }) {
                Label("Settings", systemImage: "gearshape")
            }
            Button(action: {
                presentCreate = true
            }) {
                Label("New Machine", systemImage: "plus")
            }
                    .popover(isPresented: $presentCreate) {
                        CreateContainerView(isPresented: $presentCreate, isCreating: $isCreating)
                    }
        }
        .onAppear {
            NSWindow.allowsAutomaticWindowTabbing = false
        }
        .task {
            await model.initLaunch()
        }
        .alert(isPresented: errorPresented, error: model.error) { _ in
            Button("OK") {
                model.error = nil

                // quit if the error is fatal
                if model.state == .stopped {
                    NSApp.terminate(nil)
                }
            }
        } message: { error in
            if let msg = error.recoverySuggestion {
                Text(msg)
            }
        }
    }
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
    }
}
