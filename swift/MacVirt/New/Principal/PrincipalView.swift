//
//  PrincipalView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import SwiftUI

class PrincipalViewController: NSViewController {
    var onTabChange: ((NavTabId) -> Void)?

    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let contentView = PrincipalView { [weak self] in
            guard let self else { return }
            self.onTabChange?($0)
        }
        let hostingView = NSHostingView(rootView: contentView)
        view = hostingView
    }
}

struct PrincipalView: View {
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel

    var onTabChange: (NavTabId) -> Void

    var body: some View {
        Group {
            switch model.selectedTab {
            case .dockerContainers:
                DockerContainersRootView(selection: model.initialDockerContainerSelection)
            case .dockerVolumes:
                DockerVolumesRootView()
            case .dockerImages:
                DockerImagesRootView()
            case .k8sPods:
                K8SPodsView()
            case .k8sServices:
                K8SServicesView()
            case .machines:
                MachinesRootView()
            case .cli:
                CommandsRootView()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear {
            onTabChange(model.selectedTab)
        }
        .onChange(of: model.selectedTab) { tab in
            onTabChange(tab)
        }
        .onPreferenceChange(InspectorSelectionKey.self) { value in
            navModel.inspectorSelection = value
        }
    }
}
