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
        if #available(macOS 14, *) {
            sidebarContents14
        } else {
            sidebarContents12
        }
    }
}

extension SidebarView {
    @ViewBuilder
    private var sidebarContents12: some View {
        let selBinding = Binding<NewToolbarIdentifier?>(get: {
            model.selection
        }, set: {
            if let sel = $0 {
                model.selection = sel
            }
        })

        // List(selection:) should NOT be used for navigation: https://kean.blog/post/triple-trouble
        // it's a bit buggy when programmatically controlling selection (can have two nav links showing up as active at the same time for a few frames)
        // but...
        // the alternatives, NavigationLink(tag:selection:destination:label:), and NavigationLink(isActive:destination:label:), are more buggy
        // if you hold up and down arrow keys, it consistently crashes on macOS 13.6 when transitioning between k8s pods/services tabs (when k8s is diasbled)
        // so we still have to use this "wrong" method
        // NavigationSplitView has no such bug, but it has the issue with slow sidebar show/hide
        List(selection: selBinding) {
            listContents
        }
        .listStyle(.sidebar)
        // "Personal use only" subheadline
        .safeAreaInset(edge: .bottom, alignment: .leading, spacing: 0) {
            UserSwitcherButton(presentAuth: $model.presentAuth)
        }
    }

    @available(macOS 14, *)
    @ViewBuilder private var sidebarContents14: some View {
        List(selection: $model.selection) {
            listContents
        }
        .listStyle(.sidebar)
        // "Personal use only" subheadline
        .safeAreaInset(edge: .bottom, alignment: .leading, spacing: 0) {
            UserSwitcherButton(presentAuth: $model.presentAuth)
        }
    }

    @ViewBuilder var listContents: some View {
        Section(header: Text("Docker")) {
            NavTab("Containers", systemImage: "shippingbox")
                .tag(NewToolbarIdentifier.containers)

            NavTab("Volumes", systemImage: "externaldrive")
                .tag(NewToolbarIdentifier.volumes)

            NavTab("Images", systemImage: "doc.zipper")
                .tag(NewToolbarIdentifier.images)
        }

        Section(header: Text("Kubernetes")) {
            NavTab("Pods", systemImage: "helm")
                .tag(NewToolbarIdentifier.pods)

            NavTab("Services", systemImage: "network")
                .tag(NewToolbarIdentifier.services)
        }

        Section(header: Text("Linux")) {
            NavTab("Machines", systemImage: "desktopcomputer")
                .tag(NewToolbarIdentifier.machines)
        }

        Section(header: Text("Help")) {
            NavTab("Commands", systemImage: "terminal")
                .tag(NewToolbarIdentifier.commands)
        }
    }
}
