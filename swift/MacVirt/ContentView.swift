//
//  ContentView.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: VmViewModel

    @SceneStorage("root.selectedTab") private var selection: String = "machines"
    @State private var startStopInProgress = false

    var body: some View {
        let errorPresented = Binding<Bool>(get: {
            model.error != nil
        }, set: {
            if !$0 {
                model.error = nil
            }
        })

        NavigationView {
            let selBinding = Binding<String?>(get: {
                selection
            }, set: {
                selection = $0 ?? "machines"
            })
            List(selection: selBinding) {
                NavigationLink(destination: DockerRootView()) {
                    Label("Docker", systemImage: "shippingbox")
                }.tag("docker")

                NavigationLink(destination: MachinesRootView()) {
                    Label("Machines", systemImage: "desktopcomputer")
                }.tag("machines")

                NavigationLink(destination: MachinesRootView()) {
                    Label("Commands", systemImage: "terminal")
                }.tag("terminal")
            }
                    .listStyle(.sidebar)
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                Button(action: toggleSidebar, label: {
                    Image(systemName: "sidebar.leading")
                })
                        .help("Toggle sidebar")
            }

            ToolbarItem(placement: .automatic) {
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
                .help(model.state == .running ? "Stop everything" : "Start everything")
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    if #available(macOS 13, *) {
                        NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
                    } else {
                        NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
                    }
                }) {
                    Label("Settings", systemImage: "gearshape")
                }
                .help("Settings")
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

    private func toggleSidebar() {
        NSApp.sendAction(#selector(NSSplitViewController.toggleSidebar(_:)), to: nil, from: nil)
    }
}

struct ContentView_Previews: PreviewProvider {
    static var previews: some View {
        ContentView()
    }
}
