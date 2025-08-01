//
//  NewMainView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 11/23/23.
//

import Defaults
import SwiftUI
import UserNotifications

struct NewMainView: View {
    @Environment(\.openWindow) private var openWindow
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var actionTracker: ActionTracker

    // must be StateObject to share this initial instance
    // otherwise it's different at NewMainViewControllerRepresentable() init time and at later .environmentObject time
    @StateObject private var navModel = MainNavViewModel()

    @Default(.selectedTab) var selectedTab

    var body: some View {
        GeometryReader { geometry in
            NewMainViewControllerRepresentable(
                size: geometry.size,
                model: model,
                navModel: navModel,
                actionTracker: actionTracker
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .environmentObject(navModel)
        }
        .ignoresSafeArea()
        .sheet(isPresented: $model.presentCreateMachine) {
            CreateMachineView(isPresented: $model.presentCreateMachine)
        }
        .sheet(
            isPresented: Binding(
                get: { model.presentImportMachine != nil },
                set: { if !$0 { model.presentImportMachine = nil } })
        ) {
            ImportMachineView()
        }
        .sheet(
            isPresented: Binding(
                get: { model.presentImportVolume != nil },
                set: { if !$0 { model.presentImportVolume = nil } })
        ) {
            ImportVolumeView()
        }
        .onAppear {
            // DO NOT use .task{} here.
            // start tasks should NOT be canceled
            Task { @MainActor in
                let center = UNUserNotificationCenter.current()
                do {
                    let granted = try await center.requestAuthorization(options: [
                        .alert, .sound, .badge,
                    ])
                    NSLog("notification request granted: \(granted)")
                } catch {
                    NSLog("notification request failed: \(error)")
                }
            }
        }
        .sheet(isPresented: $model.presentAuth) {
            AuthView(sheetPresented: $model.presentAuth)
        }
        .onChange(of: model.presentAuth) { _, isPresented in
            if !isPresented {
                // sheet dismissed
                // reopen force SSO sign-in dialog if not signed in (i.e. auth flow dismissed)
                // onDisappear in sheet doesn't work
                model.updateForceSignIn()
            }
        }
        .onOpenURL { url in
            // for menu bar
            // TODO: unstable
            if url.pathComponents.count >= 2,
                url.pathComponents[1] == "containers" || url.pathComponents[1] == "projects"
            {
                model.initialDockerContainerSelection = [.container(id: url.pathComponents[2])]
                selectedTab = .dockerContainers
            }
        }
        // error dialog
        .akAlert(presentedValue: $model.error, style: .critical) { error in
            error.errorDescription ?? "Error"
            error.recoverySuggestion

            switch error {
            case VmError.dockerExitError, VmError.spawnExit:
                AKAlertFlags.scrollable
            case VmError.vmgrExit(let reason, _):
                if !reason.hasCustomDetails {
                    AKAlertFlags.scrollable
                }
            default:
                // always use scrollable text box for long errors
                if error.recoverySuggestion?.count ?? 0 > 1000 {
                    AKAlertFlags.scrollable
                }
            }

            switch error {
            case VmError.killswitchExpired:
                AKAlertButton("Update") {
                    NSWorkspace.openSubwindow(WindowURL.update)
                }

                AKAlertButton("Quit") {
                    model.terminateAppNow()
                }

            case VmError.wrongArch:
                AKAlertButton("Download") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/download")!)
                }

                AKAlertButton("Quit") {
                    model.terminateAppNow()
                }

            default:
                if model.state == .stopped && !model.reachedRunning {
                    AKAlertButton("Quit") {
                        model.terminateAppNow()
                    }
                } else {
                    AKAlertButton("OK")
                }

                if error.shouldShowLogs {
                    AKAlertButton("Report") {
                        openWindow(id: WindowID.bugReport)

                        // quit if the error is fatal
                        if model.state == .stopped && !model.reachedRunning {
                            model.terminateAppNow()
                        }
                    }
                } else if error.shouldShowReset {
                    AKAlertButton("Reset") {
                        openWindow(id: WindowID.resetData)
                    }
                }
            }
        }
        .onReceive(
            model.$error,
            perform: { error in
                if error == VmError.killswitchExpired {
                    // trigger updater as well
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                        NSWorkspace.openSubwindow(WindowURL.update)
                    }
                }
            }
        )
        .akAlert(isPresented: $model.presentProfileChanged) {
            "Command-Line Tools Installed"
            """
            Your shell profile (PATH) has been updated to add \(Constants.userAppName) tools.

            Restart your terminal to use the new tools.
            """
        }
        .akAlert(isPresented: $model.presentAddPaths) {
            "Install Command-Line Tools?"
            "To install \(Constants.userAppName) tools, add ~/.orbstack/bin to your shell's PATH."
        }
        .akAlert(isPresented: $model.presentForceSignIn) {
            "Sign in"
            "Your organization requires you to sign in to \(Constants.userAppName)."

            AKAlertButton("Sign In") {
                model.presentAuth = true
            }
            AKAlertButton("Quit") {
                Task {
                    // stop asynchronously in the background
                    await model.tryStop()
                    // quit app immediately so it looks fast
                    NSApp.terminate(nil)
                }
            }
        }
        .akAlert(isPresented: $model.presentRequiresLicense) {
            "Pro license required"
            "To use OrbStack Debug Shell, purchase a Pro license."

            AKAlertButton("Get Pro") {
                NSWorkspace.shared.open(URL(string: "https://orbstack.dev/pricing")!)
            }
            AKAlertButton("Cancel")
        }
        .toastOverlay()
    }
}

struct NewMainViewControllerRepresentable: NSViewControllerRepresentable {
    var size: CGSize
    var model: VmViewModel
    var navModel: MainNavViewModel
    var actionTracker: ActionTracker

    func makeNSViewController(context _: Context) -> NewMainViewController {
        let controller = NewMainViewController(
            model: model, navModel: navModel, actionTracker: actionTracker)
        controller.horizontalConstraint = controller.view.widthAnchor.constraint(
            equalToConstant: size.width)
        controller.verticalConstraint = controller.view.heightAnchor.constraint(
            equalToConstant: size.height)
        NSLayoutConstraint.activate([
            controller.horizontalConstraint,
            controller.verticalConstraint,
        ])

        return controller
    }

    func updateNSViewController(_ nsViewController: NewMainViewController, context _: Context) {
        nsViewController.horizontalConstraint.constant = size.width
        nsViewController.verticalConstraint.constant = size.height
    }
}
