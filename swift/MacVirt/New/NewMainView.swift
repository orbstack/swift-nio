//
//  NewMainView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 11/23/23.
//

import SwiftUI
import UserNotifications

struct NewMainView: View {
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    @State private var presentError = false

    var body: some View {
        GeometryReader { geometry in
            NewMainViewControllerRepresentable(
                size: geometry.size,
                model: model
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .ignoresSafeArea()
        .sheet(isPresented: $model.presentCreateMachine) {
            CreateContainerView(isPresented: $model.presentCreateMachine)
        }
        .onAppear {
            windowTracker.openMainWindowCount += 1
            model.initLaunch()

            // DO NOT use .task{} here.
            // start tasks should NOT be canceled
            Task { @MainActor in
                let center = UNUserNotificationCenter.current()
                do {
                    let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
                    NSLog("notification request granted: \(granted)")
                } catch {
                    NSLog("notification request failed: \(error)")
                }
            }
        }
        .onDisappear {
            windowTracker.openMainWindowCount -= 1
        }
        .sheet(isPresented: $model.presentAuth) {
            AuthView(sheetPresented: $model.presentAuth)
        }
        .onOpenURL { url in
            // for menu bar
            // TODO: unstable
            if url.pathComponents.count >= 2,
               url.pathComponents[1] == "containers" || url.pathComponents[1] == "projects"
            {
                model.initialDockerContainerSelection = [.container(id: url.pathComponents[2])]
                model.selection = .containers
            }
        }
        // error dialog
        .alert(isPresented: $presentError, error: model.error) { error in
            switch error {
            case VmError.killswitchExpired:
                Button("Update") {
                    NSWorkspace.openSubwindow("update")
                }

                Button("Quit") {
                    model.terminateAppNow()
                }

            case VmError.wrongArch:
                Button("Download") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/download")!)
                }

                Button("Quit") {
                    model.terminateAppNow()
                }

            default:
                if model.state == .stopped && !model.reachedRunning {
                    Button("Quit") {
                        model.terminateAppNow()
                    }
                } else {
                    Button("OK") {
                        model.dismissError()
                    }
                }

                if error.shouldShowLogs {
                    Button("Report") {
                        model.dismissError()
                        openBugReport()

                        // quit if the error is fatal
                        if model.state == .stopped && !model.reachedRunning {
                            model.terminateAppNow()
                        }
                    }
                }
            }
        } message: { error in
            if let msg = error.recoverySuggestion {
                Text(truncateError(description: msg))
            }
        }
        .onReceive(model.$error, perform: { error in
            presentError = error != nil

            if error == VmError.killswitchExpired {
                // trigger updater as well
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    NSWorkspace.openSubwindow("update")
                }
            }
        })
        .onChange(of: presentError) {
            if !$0 {
                model.dismissError()
            }
        }
        .alert("Shell profile changed", isPresented: bindOptionalBool($model.presentProfileChanged)) {} message: {
            if let info = model.presentProfileChanged {
                Text("""
                \(Constants.userAppName)’s command-line tools have been added to your PATH.
                To use them in existing shells, run the following command:

                source \(info.profileRelPath)
                """)
            }
        }
        .alert("Add tools to PATH", isPresented: bindOptionalBool($model.presentAddPaths)) {} message: {
            if let info = model.presentAddPaths {
                let list = info.paths.joined(separator: "\n")
                Text("""
                To use \(Constants.userAppName)’s command-line tools, add the following directories to your PATH:

                \(list)
                """)
            }
        }
    }
}

struct NewMainViewControllerRepresentable: NSViewControllerRepresentable {
    var size: CGSize
    var model: VmViewModel

    func makeNSViewController(context: Context) -> NewMainViewController {
        let controller = NewMainViewController(model: model)
        controller.horizontalConstraint = controller.view.widthAnchor.constraint(equalToConstant: size.width)
        controller.verticalConstraint = controller.view.heightAnchor.constraint(equalToConstant: size.height)
        NSLayoutConstraint.activate([
            controller.horizontalConstraint,
            controller.verticalConstraint
        ])

        return controller
    }

    func updateNSViewController(_ nsViewController: NewMainViewController, context: Context) {
        nsViewController.horizontalConstraint.constant = size.width
        nsViewController.verticalConstraint.constant = size.height
    }
}
