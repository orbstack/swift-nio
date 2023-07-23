//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Defaults

private class MigrationViewModel: ObservableObject {
    @Published var statusLine = "Preparing"
    @Published var progress: Double = 0
    @Published var errors = [String]()
    @Published var entityMigrationStarted = false
    @Published var done = false

    func start() {
        Task.detached { @MainActor [self] in
            let task = Process()
            task.launchPath = AppConfig.ctlExe
            // force: we do existing-data check in GUI
            task.arguments = ["docker", "migrate", "--format", "json", "--force"]

            let pipe = Pipe()
            task.standardOutput = pipe
            task.standardError = pipe

            task.terminationHandler = { process in
                let status = process.terminationStatus
                DispatchQueue.main.async { [self] in
                    if status != 0 {
                        errors = errors + ["\nFailed with status \(status)"]
                    }
                    self.done = true
                }
            }

            do {
                try task.run()
                for try await line in pipe.fileHandleForReading.bytes.lines {
                    print("[Migration] \(line)")
                    // failed to parse lines = error
                    if let json = try? JSONSerialization.jsonObject(with: line.data(using: .utf8)!, options: []) as? [String: Any] {
                        let level = json["level"] as? String ?? ""
                        let msg = json["msg"] as? String ?? ""
                        if let progress = json["progress"] as? Double {
                            self.progress = progress / 100
                        } else if let started = json["started"] as? Bool, started {
                            entityMigrationStarted = true

                            // try to refocus us and hide docker desktop window
                            if let runningApp = NSRunningApplication.runningApplications(withBundleIdentifier: "com.electron.dockerdesktop").first {
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
               vmModel.dockerVolumes?.isEmpty ?? true {
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
        .alert(model.entityMigrationStarted ? "Some data couldn’t be migrated" : "Failed to start migration",
                isPresented: $presentErrors) {
            Button("OK", role: .cancel) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                    windowHolder.window?.close()
                }
            }
        } message: {
            Text(truncateError(description: model.errors.joined(separator: "\n")))
        }
        .alert("Replace existing data?", isPresented: $presentConfirmExisting) {
            Button("Cancel", role: .cancel) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                    windowHolder.window?.close()
                }
            }
            Button("Migrate", role: .destructive) {
                model.start()
                presentConfirmExisting = false
            }
        } message: {
            Text("You already have Docker containers, volumes, or images in OrbStack. Migrating data from Docker Desktop may lead to unexpected results.")
        }
        .frame(width: 450)
    }
}
