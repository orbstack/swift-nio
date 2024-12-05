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

    // must be StateObject to share this initial instance
    // otherwise it's different at NewMainViewControllerRepresentable() init time and at later .environmentObject time
    @StateObject private var navModel = MainNavViewModel()

    @Default(.selectedTab) var selectedTab

    var body: some View {
        GeometryReader { geometry in
            NewMainViewControllerRepresentable(
                size: geometry.size,
                model: model,
                navModel: navModel
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .environmentObject(navModel)
        }
        .ignoresSafeArea()
        .sheet(isPresented: $model.presentCreateMachine) {
            CreateContainerView(isPresented: $model.presentCreateMachine)
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
        .onChange(of: model.presentAuth) { isPresented in
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
        .akAlert(presentedValue: $model.error) { error in
            var content = AKAlertContent(
                title: error.errorDescription ?? "Error",
                desc: error.recoverySuggestion,
                style: .critical)

            switch error {
            case VmError.dockerExitError, VmError.vmgrExit, VmError.spawnExit:
                content.scrollableText = true
            default:
                // always use scrollable text box for long errors
                content.scrollableText = error.recoverySuggestion?.count ?? 0 > 1000
            }

            switch error {
            case VmError.killswitchExpired:
                content.addButton("Update") {
                    NSWorkspace.openSubwindow(WindowURL.update)
                }

                content.addButton("Quit") {
                    model.terminateAppNow()
                }

            case VmError.wrongArch:
                content.addButton("Download") {
                    NSWorkspace.shared.open(URL(string: "https://orbstack.dev/download")!)
                }

                content.addButton("Quit") {
                    model.terminateAppNow()
                }

            default:
                if model.state == .stopped && !model.reachedRunning {
                    content.addButton("Quit") {
                        model.terminateAppNow()
                    }
                } else {
                    content.addButton("OK")
                }

                if error.shouldShowLogs {
                    content.addButton("Report") {
                        openWindow(id: WindowID.bugReport)

                        // quit if the error is fatal
                        if model.state == .stopped && !model.reachedRunning {
                            model.terminateAppNow()
                        }
                    }
                }
            }

            return content
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
        .akAlert(
            "Command-Line Tools Installed", isPresented: $model.presentProfileChanged,
            desc: {
                """
                Your shell profile (PATH) has been updated to add \(Constants.userAppName) tools.

                Restart your terminal to use the new tools.
                """
            }
        )
        .akAlert(
            "Install Command-Line Tools?", isPresented: $model.presentAddPaths,
            desc: {
                """
                To install \(Constants.userAppName) tools, add ~/.orbstack/bin to your shell's PATH.
                """
            }
        )
        .akAlert(
            "Sign in", isPresented: $model.presentForceSignIn,
            desc: { "Your organization requires you to sign in to \(Constants.userAppName)." },
            button1Label: "Sign In",
            button1Action: { model.presentAuth = true },
            button2Label: "Quit",
            // clean shutdown flow
            button2Action: { NSApp.terminate(nil) }
        )
        .akAlert(
            "Pro license required", isPresented: $model.presentRequiresLicense,
            desc: "To use OrbStack Debug Shell, purchase a Pro license.",
            button1Label: "Get Pro",
            button1Action: {
                NSWorkspace.shared.open(URL(string: "https://orbstack.dev/pricing")!)
            },
            button2Label: "Cancel")
    }
}

struct NewMainViewControllerRepresentable: NSViewControllerRepresentable {
    var size: CGSize
    var model: VmViewModel
    var navModel: MainNavViewModel

    func makeNSViewController(context _: Context) -> NewMainViewController {
        let controller = NewMainViewController(model: model, navModel: navModel)
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
