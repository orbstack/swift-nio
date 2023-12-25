//
//  PrincipalView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import SwiftUI

class PrincipalViewController: NSViewController {
    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let contentView = PrincipalView()
        let hostingView = NSHostingView(rootView: contentView)
        view = hostingView
    }
}

struct PrincipalView: View {
    @EnvironmentObject var model: VmViewModel

    var body: some View {
        Group {
            switch model.selection {
            case .containers:
                DockerContainersRootView(selection: model.initialDockerContainerSelection)
            case .volumes:
                DockerVolumesRootView()
            case .images:
                DockerImagesRootView()
            case .pods:
                K8SPodsView()
            case .services:
                K8SServicesView()
            case .machines:
                MachinesRootView()
            case .commands:
                CommandsRootView()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
