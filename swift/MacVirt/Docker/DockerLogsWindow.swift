//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

private let maxLines = 5000
private let maxChars = 5000 * 150 // avg line len - easier to do it like this
private let bottomScrollThreshold = 256.0
private let fontSize = 13.0

private let terminalFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
private let terminalFontBold = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)

private let urlRegex = try! NSRegularExpression(pattern: #"http(s)?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{2,256}(\.[a-z]{2,6})?\b([-a-zA-Z0-9@:%_\+.~#?&\/\/=]*)"#)
private let ansiColorRegex = try! NSRegularExpression(pattern: #"\u001B\[([0-9]{1,2};?)*?m"#)

private let ansiColorPalette: [NSColor] = [
    .textBackgroundColor, // black
    .systemRed,
    .systemGreen,
    .systemYellow,
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor, // white
]

private struct AnsiState: Equatable {
    var bold = false
    var underline = false
    var colorFg = 0
    var colorBg = 0

    func addAttribute(to: NSMutableAttributedString, range: NSRange) {
        if bold {
            to.addAttribute(.font, value: terminalFontBold, range: range)
        }
        if underline {
            to.addAttribute(.underlineStyle, value: NSUnderlineStyle.single.rawValue, range: range)
        }
        if colorFg != 0 {
            to.addAttribute(.foregroundColor, value: ansiColorPalette[colorFg], range: range)
        }
        if colorBg != 0 {
            to.addAttribute(.backgroundColor, value: ansiColorPalette[colorBg], range: range)
        }
    }
}

private class LogsViewModel: ObservableObject {
    private var seq = 0

    var contents = NSMutableAttributedString()
    let updateEvent = PassthroughSubject<(), Never>()
    let searchCommand = PassthroughSubject<(), Never>()

    private var process: Process?
    private var exited = false

    private var lastAnsiState = AnsiState()

    func start(isCompose: Bool, args: [String]) {
        print("start: \(args)")
        Task.detached { @MainActor [self] in
            print("running: \(args)")
            self.exited = false
            lastAnsiState = AnsiState()
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

            task.terminationHandler = { process in
                print("term = \(process.terminationStatus)")
                let status = process.terminationStatus
                DispatchQueue.main.async { [self] in
                    if status != 0 {
                        add(error: "Failed with status \(status)")
                    }
                    self.exited = true
                }
            }
            process = task

            do {
                print("begin")
                try task.run()
                print("r..")
                /*var line = ""
                for try await ch in pipe.fileHandleForReading.bytes.characters {
                    // \r for pty logs. mac combines them
                    if ch == "\r" || ch == "\n" || ch == "\r\n" {
                        addOutputLine(text: line)
                        line = ""
                    } else {
                        line.append(ch)
                    }
                }*/
                // this skips empty lines but is much faster
                for try await line in pipe.fileHandleForReading.bytes.lines {
                    print("line: \(line)")
                    add(terminalLine: line)
                }
            } catch {
                add(error: "Failed to run migration: \(error)")
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

    private func add(terminalLine: String) {
        let attributedStr = NSMutableAttributedString(string: terminalLine + "\n")
        // font
        attributedStr.addAttribute(.font, value: terminalFont, range: NSRange(location: 0, length: attributedStr.length))

        // parse links first, before indexes change
        var matches = urlRegex.matches(in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
        for match in matches {
            let url = (terminalLine as NSString).substring(with: match.range)
            attributedStr.addAttribute(.link, value: url, range: match.range)
        }

        // parse colors from ANSI escapes - state machine
        matches = ansiColorRegex.matches(in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
        var state = AnsiState()
        var lastI = 0
        for match in matches {
            // ranges aren't repeated properly, so do it ourselves
            let codes = (terminalLine as NSString).substring(with: match.range)
                .replacingOccurrences(of: "\u{001B}[", with: "")
                .replacingOccurrences(of: "m", with: "")
                .split(separator: ";")
            // empty ESC[m = reset
            if codes.isEmpty {
                state = AnsiState()
            }

            for codeStr in codes {
                let code = Int(codeStr)
                guard let code else {
                    // failed to parse
                    continue
                }

                switch code {
                case 0:
                    // reset
                    state = AnsiState()
                case 1:
                    state.bold = true
                case 4:
                    state.underline = true
                case 30...37:
                    state.colorFg = code - 30
                case 39:
                    state.colorFg = 0
                case 40...47:
                    state.colorBg = code - 40
                case 49:
                    state.colorBg = 0
                // bright = bold + color
                case 90...97:
                    state.colorFg = code - 90
                    state.bold = true
                case 100...107:
                    state.colorBg = code - 100
                    state.bold = true
                default:
                    continue
                }
            }

            // state updated. add last mark
            if state != lastAnsiState {
                lastAnsiState.addAttribute(to: attributedStr, range: NSRange(location: lastI, length: match.range.location - lastI))
                lastAnsiState = state
                lastI = match.range.location
            }
        }
        // add terminating mark
        state.addAttribute(to: attributedStr, range: NSRange(location: lastI, length: terminalLine.utf16.count - lastI))
        lastAnsiState = state
        // then delete escapes
        for match in matches.reversed() {
            attributedStr.deleteCharacters(in: match.range)
        }

        add(attributedString: attributedStr)
    }

    private func add(attributedString: NSMutableAttributedString) {
        seq += 1
        contents.append(attributedString)
        // truncate if needed
        if contents.length > maxChars {
            contents.deleteCharacters(in: NSRange(location: 0, length: contents.length - maxChars))
        }
        // publish
        updateEvent.send()
    }

    private func add(error: String) {
        let str = NSMutableAttributedString(string: error + "\n")
        // bold font
        str.addAttribute(.font, value: terminalFontBold, range: NSRange(location: 0, length: str.length))
        // red
        str.addAttribute(.foregroundColor, value: NSColor.systemRed, range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    func clear() {
        contents = NSMutableAttributedString()
        updateEvent.send()
    }

    func copyAll() {
        NSPasteboard.copy(contents.string)
    }
}

private struct LogsTextView: NSViewRepresentable {
    @ObservedObject var model: LogsViewModel

    class Coordinator: NSObject, NSTableViewDelegate, NSTableViewDataSource {
        var cancellables = Set<AnyCancellable>()
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        let textView = scrollView.documentView as! NSTextView

        // textView.font and textView.isAutomaticLinkDetectionEnabled don't work
        textView.textContainerInset = NSSize(width: 8, height: 8)
        textView.usesAdaptiveColorMappingForDarkAppearance = true
        textView.isAutomaticDataDetectionEnabled = false
        textView.isIncrementalSearchingEnabled = true

        // char wrap, line height
        let paragraphStyle = NSMutableParagraphStyle.default.mutableCopy() as! NSMutableParagraphStyle
        paragraphStyle.lineBreakMode = .byCharWrapping
        paragraphStyle.lineHeightMultiple = 1.2
        textView.defaultParagraphStyle = paragraphStyle

        textView.isEditable = false
        textView.usesFindBar = true

        model.updateEvent
            .throttle(for: 0.035, scheduler: DispatchQueue.main, latest: true)
            .sink { [weak textView] _ in
                guard let textView else { return }
                // TODO only scroll if at bottom
                //let shouldScroll = abs(textView.visibleRect.maxY - textView.bounds.maxY) < bottomScrollThreshold
                textView.textStorage?.setAttributedString(model.contents)

                NSAnimationContext.beginGrouping()
                NSAnimationContext.current.duration = 0
                textView.scrollToEndOfDocument(nil)
                NSAnimationContext.endGrouping()
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
            } else {
                ContentUnavailableViewCompat("Container Removed", systemImage: "trash", desc: "No logs available.")
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
