//
// Created by Danny Lin on 5/7/23.
//

import Defaults
import Foundation
import Sentry
import SwiftUI

private class MigrationViewModel: ObservableObject {
    @Published var statusLine = "Preparing"
    @Published var progress: Double = 0
    @Published var errors = [String]()
    @Published var entityMigrationStarted = false
    @Published var done = false

    func start() {
        Task.detached { @MainActor [self] in
            var exitStatus = -1
            defer {
                if done && !errors.isEmpty {
                    SentrySDK.capture(
                        error: VmError.dockerMigrationError(
                            status: exitStatus, output: errors.joined(separator: "\n")))
                }
            }

            let task = Process()
            task.launchPath = AppConfig.ctlExe
            // force: we do existing-data check in GUI
            task.arguments = ["migrate", "docker", "--format", "json", "--force"]

            let pipe = Pipe()
            task.standardOutput = pipe
            task.standardError = pipe

            task.terminationHandler = { process in
                let status = process.terminationStatus
                DispatchQueue.main.async { [self] in
                    // 1 is normal for Compose on EOF (when dockerd stops)
                    // ignore it for now so people don't see the error
                    if status != 0 && status != 1 {
                        errors = errors + ["\nFailed with status \(status)"]
                    }
                    exitStatus = Int(status)
                    self.done = true
                }
            }

            do {
                try task.run()
                for try await line in pipe.fileHandleForReading.bytes.lines {
                    print("[Migration] \(line)")
                    // failed to parse lines = error
                    if let json = try? JSONSerialization.jsonObject(
                        with: line.data(using: .utf8)!, options: []) as? [String: Any]
                    {
                        let level = json["level"] as? String ?? ""
                        let msg = json["msg"] as? String ?? ""
                        if let progress = json["progress"] as? Double {
                            self.progress = progress / 100
                        } else if let started = json["started"] as? Bool, started {
                            entityMigrationStarted = true

                            // try to refocus us and hide docker desktop window
                            if let runningApp = NSRunningApplication.runningApplications(
                                withBundleIdentifier: "com.electron.dockerdesktop"
                            ).first {
                                runningApp.hide()
                            }
                            NSApp.activate(ignoringOtherApps: true)
                        } else if level == "error" {
                            errors = errors + [msg]
                        } else if level == "info" {
                            statusLine = msg
                        }
                    } else {
                        errors = errors + [line]
                    }
                }
            } catch {
                errors = errors + ["Failed to run migration: \(error)"]
                self.done = true
            }
        }
    }
}

struct DockerMigrationWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()
    @StateObject private var model = MigrationViewModel()

    @State private var presentErrors = false
    @State private var presentConfirmExisting = false

    var body: some View {
        VStack(alignment: .leading) {
            Text(model.statusLine)
                .lineLimit(1)

            ProgressView(value: model.progress)
        }
        .padding(16)
        .background(WindowAccessor(holder: windowHolder))
        .onAppear {
            if let window = windowHolder.window {
                window.isRestorable = false
            }

            if vmModel.dockerContainers?.isEmpty ?? true,
                vmModel.dockerImages?.isEmpty ?? true,
                vmModel.dockerVolumes?.isEmpty ?? true
            {
                // empty = OK to start
                model.start()
            } else {
                // not empty - ask for confirmation
                presentConfirmExisting = true
            }
        }
        .onChange(of: windowHolder.window) { window in
            if let window {
                // unrestorable: is ephemeral, and also restored doesn't preserve url
                window.isRestorable = false
            }
        }
        .onChange(of: model.done) { done in
            if done {
                if model.errors.isEmpty {
                    // successful migration counts as dismissed
                    Defaults[.dockerMigrationDismissed] = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                        windowHolder.window?.close()
                    }
                } else {
                    presentErrors = true
                }
            }
        }
        .akAlert(
            model.entityMigrationStarted
                ? "Some data couldn’t be migrated" : "Failed to start migration",
            isPresented: $presentErrors,
            desc: { truncateError(description: model.errors.joined(separator: "\n")) },
            button1Label: "OK",
            button1Action: {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                    windowHolder.window?.close()
                }
            }
        )
        .akAlert(
            "Replace existing data?", isPresented: $presentConfirmExisting,
            desc:
                "You already have Docker containers, volumes, or images in OrbStack. Migrating data from Docker Desktop may lead to unexpected results.",
            button1Label: "Migrate",
            button1Action: {
                model.start()
            },
            button2Label: "Cancel",
            button2Action: {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                    windowHolder.window?.close()
                }
            }
        )
        .frame(width: 450)
    }
}
