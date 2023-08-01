//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

private let maxLines = 5000

private class LogsViewModel: ObservableObject {
    private var seq = 0

    @Published var contents = NSMutableAttributedString()
    let searchCommand = PassthroughSubject<(), Never>()

    private var process: Process?
    private var exited = false

    func start(isCompose: Bool, args: [String]) {
        print("start: \(args)")
        Task.detached { @MainActor [self] in
            print("running: \(args)")
            let task = Process()
            task.launchPath = isCompose ? AppConfig.dockerComposeExe : AppConfig.dockerExe
            // force: we do existing-data check in GUI
            task.arguments = args

            // env is more robust, user can mess with context
            var newEnv = ProcessInfo.processInfo.environment
            newEnv["TERM"] = "xterm" // 16 color only
            newEnv["DOCKER_HOST"] = "unix://\(Files.dockerSocket)"
            task.environment = newEnv

            let pipe = Pipe()
            task.standardOutput = pipe
            task.standardError = pipe

            //TODO terminate on disappear
            task.terminationHandler = { process in
                print("term = \(process.terminationStatus)")
                let status = process.terminationStatus
                DispatchQueue.main.async { [self] in
                    if status != 0 {
                        addError("Failed with status \(status)")
                    }
                    self.exited = true
                }
            }
            process = task

            do {
                print("begin")
                try task.run()
                print("r..")
                for try await line in pipe.fileHandleForReading.bytes.lines {
                    addOutputLine(text: line)
                }
            } catch {
                addError("Failed to run migration: \(error)")
                self.exited = true
            }
        }
    }

    func stop() {
        if let process {
            process.terminate()
        }
        process = nil
    }

    private func addOutputLine(text: String) {
        var str = AttributedString(text)

        // TODO parse colors
        // TODO parse links

        addLine(text: str)
    }

    private func addLine(text: AttributedString) {
        seq += 1
        let tmp = NSMutableAttributedString(text + "\n")
        // font
        tmp.addAttribute(.font, value: NSFont.monospacedSystemFont(ofSize: 12, weight: .regular), range: NSRange(location: 0, length: tmp.length))
        contents.append(tmp)
        // publish
        contents = contents
    }

    private func addError(_ text: String) {
        var str = AttributedString(text)
        str.foregroundColor = .red
        str.font = .system(size: 12).bold()
        addLine(text: str)
    }

    func clear() {
        contents = NSMutableAttributedString()
    }

    func copyAll() {
        //TODO Excl time
        NSPasteboard.copy(contents.string)
    }
}

private struct LogsTextView: NSViewRepresentable {
    @ObservedObject var model: LogsViewModel

    private let timeColumn = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("time"))
    private let msgColumn = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("msg"))

    final class Coordinator: NSObject, NSTableViewDelegate, NSTableViewDataSource {
        var cancellables = Set<AnyCancellable>()
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        let textView = scrollView.documentView as! NSTextView
        textView.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        textView.usesAdaptiveColorMappingForDarkAppearance = true
        textView.isAutomaticDataDetectionEnabled = false
        textView.isIncrementalSearchingEnabled = true

        textView.isEditable = false
        textView.usesFindBar = true

        model.$contents
        .throttle(for: 0.05, scheduler: DispatchQueue.main, latest: true)
        .sink { [weak textView] newContents in
            guard let textView else { return }
            textView.textStorage?.setAttributedString(newContents)
            // scroll to bottom
//            NSAnimationContext.beginGrouping()
//            NSAnimationContext.current.duration = 0
//            textView.scrollToEndOfDocument(nil)
//            NSAnimationContext.endGrouping()
            textView.scrollToEndOfDocument(nil)
//            if let scrollView = textView.enclosingScrollView {
//                let point = NSPoint(x: 0, y: textView.frame.height - scrollView.contentSize.height)
//                scrollView.contentView.scroll(to: point)
//            }
        }.store(in: &context.coordinator.cancellables)

        model.searchCommand.sink { [weak textView] query in
            guard let textView else {
                return
            }
            // need .tag holder
            let button = NSButton()
            button.tag = NSTextFinder.Action.showFindInterface.rawValue
            textView.performFindPanelAction(button)
        }.store(in: &context.coordinator.cancellables)

        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }
}

private struct LogsView: View {
    let isCompose: Bool
    let args: [String]
    @StateObject var model: LogsViewModel

    var body: some View {
        LogsTextView(model: model)
        .onAppear {
            model.start(isCompose: isCompose, args: args)
        }
        .onDisappear {
            model.stop()
        }
        .onChange(of: args) { newArgs in
            model.clear()
            //model.start(isCompose: isCompose, args: newArgs)
        }
    }
}

struct DockerLogsWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()
    @StateObject private var model = LogsViewModel()

    @State private var containerId: String?
    @State private var composeProject: String?

    // persist if somehow window gets restored
    @SceneStorage("DockerLogs_url") private var savedUrl: URL?

    var body: some View {
        Group {
            if let containerId,
               let containers = vmModel.dockerContainers,
               let container = containers.first(where: { $0.id == containerId }) {
                LogsView(isCompose: false,
                        args: ["logs", "-f", "-n", String(maxLines), containerId],
                        model: model)
                .navigationTitle("Logs: \(container.userName)")
            } else if let composeProject {
                LogsView(isCompose: true,
                        args: ["-p", composeProject, "logs", "-f", "-n", String(maxLines)],
                        model: model)
                .navigationTitle("Project Logs: \(composeProject)")
            }
        }
        .onOpenURL { url in
            onOpenURL(url)
        }
        .task {
            if let savedUrl {
                onOpenURL(savedUrl)
            }
        }
        .onDisappear {
            if let containerId {
                vmModel.openLogWindowIds.remove(containerId)
            } else if let composeProject {
                vmModel.openLogWindowIds.remove(composeProject)
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .onAppear {
            if let window = windowHolder.window {
                window.isRestorable = false
            }
        }
        .onChange(of: windowHolder.window) { window in
            if let window {
                // unrestorable: is ephemeral, and also restored doesn't preserve url
                window.isRestorable = false
            }
        }
        .frame(minWidth: 400, minHeight: 200)
        // clear toolbar
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button(action: {
                    model.copyAll()
                }) {
                    Label("Copy", systemImage: "doc.on.doc")
                }
                .disabled(containerId == nil && composeProject == nil)
                .help("Copy")
                .keyboardShortcut("c", modifiers: [.command, .shift])
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    model.clear()
                }) {
                    Label("Clear", systemImage: "trash")
                }
                .disabled(containerId == nil && composeProject == nil)
                .help("Clear")
                .keyboardShortcut("k", modifiers: [.command])
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    model.searchCommand.send()
                }) {
                    Label("Search", systemImage: "magnifyingglass")
                }
                .disabled(containerId == nil && composeProject == nil)
                .help("Search")
            }
        }
    }

    private func onOpenURL(_ url: URL) {
        if url.pathComponents[1] == "project-logs" {
            composeProject = url.lastPathComponent
            vmModel.openLogWindowIds.insert(composeProject!)
        } else {
            containerId = url.lastPathComponent
            vmModel.openLogWindowIds.insert(containerId!)
        }
        savedUrl = url
    }
}
