//
//  SidebarView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import Defaults
import SwiftUI

class SidebarViewController: NSViewController {
    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let contentView = SidebarView()
        let hostingView = NSHostingView(rootView: contentView)
        view = hostingView
    }
}

struct SidebarView: View {
    @EnvironmentObject var model: VmViewModel

    @Default(.selectedTab) private var defaultsSelectedTab

    // macOS <13 requires nullable selection
    @State private var selectedTab: NavTabId? = Defaults[.selectedTab]

    var body: some View {
        List(selection: $selectedTab) {
            listContents
        }
        .onChange(of: defaultsSelectedTab) {
            selectedTab = $0
        }
        .onChange(of: selectedTab) {
            if let sel = $0, sel != Defaults[.selectedTab] {
                Defaults[.selectedTab] = sel
            }
        }
        .listStyle(.sidebar)
        // "Personal use only" subheadline
        .safeAreaInset(edge: .bottom, alignment: .leading, spacing: 0) {
            UserSwitcherButton(presentAuth: $model.presentAuth)
        }
    }
}

extension SidebarView {
    @ViewBuilder var listContents: some View {
        Section(header: Text("Docker")) {
            NavTab("Containers", systemImage: "shippingbox")
                .tag(NavTabId.dockerContainers)

            NavTab("Volumes", systemImage: "externaldrive")
                .tag(NavTabId.dockerVolumes)

            NavTab("Images", systemImage: "doc.zipper")
                .tag(NavTabId.dockerImages)

            NavTab("Networks", systemImage: "point.3.filled.connected.trianglepath.dotted")
                .tag(NavTabId.dockerNetworks)
        }

        Section(header: Text("Kubernetes")) {
            NavTab("Pods", systemImage: "helm")
                .tag(NavTabId.k8sPods)

            NavTab("Services", systemImage: "network")
                .tag(NavTabId.k8sServices)
        }

        Section(header: Text("Linux")) {
            NavTab("Machines", systemImage: "desktopcomputer")
                .tag(NavTabId.machines)
        }

        Section(header: Text("General")) {
            NavTab("Activity Monitor", systemImage: "chart.xyaxis.line")
                .tag(NavTabId.activityMonitor)

            NavTab("Commands", systemImage: "terminal")
                .tag(NavTabId.cli)
        }
    }
}
