import SwiftUI

struct K8SPodLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @StateObject private var commandModel = CommandViewModel()

    let pod: K8SPod

    var body: some View {
        K8SLogsContentView(kid: pod.id, containerName: nil)
            // render under toolbar
            .ignoresSafeArea()
            .environmentObject(commandModel)
            .onReceive(vmModel.toolbarActionRouter) { action in
                if action == .k8sPodOpenInNewWindow {
                    pod.showLogs(windowTracker: windowTracker)
                }
            }
    }
}
