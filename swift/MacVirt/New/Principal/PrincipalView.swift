//
//  PrincipalView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import Defaults
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

    @Default(.selectedTab) private var selectedTab

    var onTabChange: (NavTabId) -> Void

    var body: some View {
        Group {
            switch selectedTab {
            case .dockerContainers:
                DockerContainersRootView(selection: model.initialDockerContainerSelection)
            case .dockerVolumes:
                DockerVolumesRootView()
            case .dockerImages:
                DockerImagesRootView()
            case .dockerNetworks:
                DockerNetworksRootView()
            case .k8sPods:
                K8SPodsView()
            case .k8sServices:
                K8SServicesView()
            case .machines:
                MachinesRootView()
            case .cli:
                CommandsRootView()
            case .activityMonitor:
                ActivityMonitorRootView()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onChange(of: selectedTab, initial: true) { oldTab, tab in
            if tab != oldTab {
                // clear inspector view when tab changes,
                // but don't clear state on setting same value (ie gui re-open)
                navModel.inspectorView = nil
                navModel.inspectorSelection = []
            }

            onTabChange(tab)
        }
        .onPreferenceChange(InspectorSelectionKey.self) { value in
            navModel.inspectorSelection = value
        }
        .onPreferenceChange(InspectorViewKey.self) { value in
            navModel.inspectorView = value
        }
    }
}
