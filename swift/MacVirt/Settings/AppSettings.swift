//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import Sparkle
import SwiftUI

// TODO: remove dep on service
typealias SettingsStateWrapperView = StateWrapperView

struct AppSettings: View {
    let updaterController: SPUStandardUpdaterController

    @State private var selectedTab: Tabs = .general

    private enum Tabs: Hashable {
        case general
        case machine
        case docker
        case k8s
        case network
        case storage
        #if DEBUG
            case debug
        #endif
    }

    var body: some View {
        NavigationSplitView(columnVisibility: .constant(.all)) {
            List(selection: $selectedTab) {
                Section {
                    Label("General", systemImage: "gear")
                        .tag(Tabs.general)
                    Label("System", systemImage: "cpu")
                        .tag(Tabs.machine)
                    Label("Network", systemImage: "network")
                        .tag(Tabs.network)
                    Label("Storage", systemImage: "externaldrive")
                        .tag(Tabs.storage)
                }

                Section {
                    Label("Docker", systemImage: "shippingbox")
                        .tag(Tabs.docker)
                    Label("Kubernetes", systemImage: "helm")
                        .tag(Tabs.k8s)
                }

                #if DEBUG
                    Section {
                        Label("Debug", systemImage: "hammer")
                            .tag(Tabs.debug)
                    }
                #endif
            }
            .navigationSplitViewColumnWidth(150)
        } detail: {
            NavigationStack {
                switch selectedTab {
                case .general:
                    GeneralSettingsView(updaterController: updaterController)
                case .machine:
                    MachineSettingsView()
                case .docker:
                    DockerSettingsView()
                case .k8s:
                    K8SSettingsView()
                case .network:
                    NetworkSettingsView()
                case .storage:
                    StorageSettingsView()
                #if DEBUG
                    case .debug:
                        DebugSettingsView(updaterController: updaterController)
                #endif
                }
            }
        }
        .toolbarRemovingSidebarToggleCompat()
        .navigationSplitViewStyle(.prominentDetail)
        .onWindowReady { window in
            window.toolbarStyle = .unified
        }
        .frame(width: 650, height: 600)
        .navigationTitle("Settings")
    }
}
