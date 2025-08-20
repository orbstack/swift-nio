import SwiftUI

struct K8SPodLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    let pod: K8SPod

    var body: some View {
        LogsTabToolbarWrapper {
            K8SLogsContentView(kid: pod.id, containerName: nil)
                .onReceive(vmModel.toolbarActionRouter) { action in
                    if action == .k8sPodOpenInNewWindow {
                        pod.showLogs(windowTracker: windowTracker)
                    }
                }
        }
    }
}
