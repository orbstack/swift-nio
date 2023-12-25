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

    var body: some View {
        // macOS <13 requires nullable selection
        let selBinding = Binding<NavTabId?>(get: {
            model.selection
        }, set: {
            if let sel = $0 {
                model.selection = sel
            }
        })

        List(selection: selBinding) {
            listContents
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

        Section(header: Text("Help")) {
            NavTab("Commands", systemImage: "terminal")
                .tag(NavTabId.cli)
        }
    }
}
