//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

private let maxLines = 5000
private let maxChars = maxLines * 150 // avg line len - easier to do it like this
private let bottomScrollThreshold = 256.0
private let fontSize = 12.5
private let terminalLineHeight = 1.2

private let terminalFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
private let terminalFontBold = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
private let terminalColor = NSColor.textColor

private let urlRegex = try! NSRegularExpression(pattern: #"http(s)?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{2,256}(\.[a-z]{2,6})?\b([-a-zA-Z0-9@:%_\+.~#?&\/\/=]*)"#)
private let ansiColorRegex = try! NSRegularExpression(pattern: #"\u001B\[([0-9]{1,2};?)*?m"#)

private let ansiColorPalette: [NSColor] = [
    // keep in mind that ansi colors are meant for white-on-black
    .textBackgroundColor, // black
    .systemRed,
    .systemGreen,
    .systemOrange, // systemYellow has bad contrast in light
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor, // white
]

private struct AnsiState: Equatable {
    var bold = false
    var underline = false
    var colorFg: Int?
    var colorBg: Int?

    func addAttribute(to: NSMutableAttributedString, range: NSRange) {
        if bold {
            to.addAttribute(.font, value: terminalFontBold, range: range)
        }
        if underline {
            to.addAttribute(.underlineStyle, value: NSUnderlineStyle.single.rawValue, range: range)
        }
        if let colorFg {
            to.addAttribute(.foregroundColor, value: ansiColorPalette[colorFg], range: range)
        }
        if let colorBg {
            to.addAttribute(.backgroundColor, value: ansiColorPalette[colorBg], range: range)
        }
    }
}

private class PtyPipe: Pipe {
    private let _fileHandleForReading: FileHandle
    private let _fileHandleForWriting: FileHandle

    override init() {
        let masterFd = posix_openpt(O_CLOEXEC)
        if masterFd == -1 {
            // fallback = pipe
            let pipe = Pipe()
            _fileHandleForReading = pipe.fileHandleForReading
            _fileHandleForWriting = pipe.fileHandleForWriting
            return
        }
        grantpt(masterFd)
        unlockpt(masterFd)

        let slaveFd = open(ptsname(masterFd), O_RDWR | O_NOCTTY | O_CLOEXEC)
        guard slaveFd != -1 else {
            close(masterFd)
            // fallback = pipe
            let pipe = Pipe()
            _fileHandleForReading = pipe.fileHandleForReading
            _fileHandleForWriting = pipe.fileHandleForWriting
            return
        }

        // use FileHandle immediately
        _fileHandleForReading = FileHandle(fileDescriptor: masterFd, closeOnDealloc: true)
        _fileHandleForWriting = FileHandle(fileDescriptor: slaveFd, closeOnDealloc: true)
    }

    override var fileHandleForReading: FileHandle {
        _fileHandleForReading
    }

    override var fileHandleForWriting: FileHandle {
        _fileHandleForWriting
    }
}

private class AsyncPipeReader {
    private var buf = Data(capacity: 1024)
    private var lastCh: UInt8 = 0

    private let pipe: Pipe
    private let callback: (String) -> Void

    init(pipe: Pipe, callback: @escaping (String) -> Void) {
        self.pipe = pipe
        self.callback = callback
        pipe.fileHandleForReading.readabilityHandler = onReadable
    }

    private func onReadable(handle: FileHandle) {
        for ch in handle.availableData {
            // \r for pty logs
            if ch == 10 || ch == 13 { // \n or \r
                if lastCh == 13 {
                    // skip \n after \r
                    lastCh = ch
                    continue
                }

                buf.append(10) // \n
                callback(String(decoding: buf, as: UTF8.self))
                buf.removeAll(keepingCapacity: true)
            } else {
                buf.append(ch)
            }

            lastCh = ch
        }
    }

    func finish() {
        pipe.fileHandleForReading.readabilityHandler = nil
        // drain
        onReadable(handle: pipe.fileHandleForReading)
    }
}

private class LogsViewModel: ObservableObject {
    var contents = NSMutableAttributedString()
    let updateEvent = PassthroughSubject<(), Never>()
    let searchCommand = PassthroughSubject<(), Never>()

    var process: Process?
    private var lastAnsiState = AnsiState()
    private var isFirstStart = true

    var cancellables = Set<AnyCancellable>()

    private var lastIsCompose: Bool?
    private var lastArgs: [String]?
    private var lastLineDate: Date?

    @MainActor
    func start(isCompose: Bool, args: [String]) {
        // reset first
        stop()
        lastAnsiState = AnsiState()
        lastIsCompose = isCompose
        lastArgs = args
        // append arg to filter since last received line, for restart
        var args = args
        if let lastLineDate {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions.insert(.withFractionalSeconds)
            args.append("--since=\(formatter.string(from: lastLineDate))")
        }

        // if not first start, add delimiter
        if !isFirstStart {
            addDelimiter()
        }
        isFirstStart = false

        let task = Process()
        task.launchPath = isCompose ? AppConfig.dockerComposeExe : AppConfig.dockerExe
        // force: we do existing-data check in GUI
        task.arguments = args

        // env is more robust, user can mess with context
        var newEnv = ProcessInfo.processInfo.environment
        newEnv["TERM"] = "xterm" // 16 color only
        newEnv["DOCKER_HOST"] = "unix://\(Files.dockerSocket)"
        task.environment = newEnv

        // use pty to make docker-compose print colored prefixes
        let pipe = PtyPipe()
        task.standardOutput = pipe
        task.standardError = pipe
        // AsyncBytes is not actually async, it blocks on read and occupies a task thread
        // so can't run multiple tasks concurrently
        let reader = AsyncPipeReader(pipe: pipe) { line in
            // this queuing actually improves perf and provides a buffer:
            // if gui is slow it'll update less often but won't block the reader
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                lastLineDate = Date() // for restart
                add(terminalLine: line)
            }
        }

        task.terminationHandler = { process in
            let status = process.terminationStatus
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }

                reader.finish()
                // mark as exited for restarting on container state change
                self.process = nil
                if status != 0 {
                    add(error: "Failed with status \(status)")
                }
            }
        }
        process = task

        do {
            try task.run()
        } catch {
            add(error: "Failed to start log stream: \(error)")
        }
    }

    func stop() {
        if let process {
            process.terminate()
        }
        process = nil

        // don't restart
        lastIsCompose = nil
        lastArgs = nil
    }

    @MainActor
    func restart() {
        if let lastIsCompose, let lastArgs {
            start(isCompose: lastIsCompose, args: lastArgs)
        }
    }

    @MainActor
    private func add(terminalLine: String) {
        let attributedStr = NSMutableAttributedString(string: terminalLine)
        // font
        attributedStr.addAttribute(.font, value: terminalFont, range: NSRange(location: 0, length: attributedStr.length))
        // color
        attributedStr.addAttribute(.foregroundColor, value: terminalColor, range: NSRange(location: 0, length: attributedStr.length))

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
                    state.colorFg = nil
                case 40...47:
                    state.colorBg = code - 40
                case 49:
                    state.colorBg = nil
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

    @MainActor
    private func add(attributedString: NSMutableAttributedString) {
        contents.append(attributedString)
        // truncate if needed
        if contents.length > maxChars {
            contents.deleteCharacters(in: NSRange(location: 0, length: contents.length - maxChars))
        }
        // publish
        updateEvent.send()
    }

    @MainActor
    private func add(error: String) {
        let str = NSMutableAttributedString(string: error + "\n")
        // bold font
        str.addAttribute(.font, value: terminalFontBold, range: NSRange(location: 0, length: str.length))
        // red
        str.addAttribute(.foregroundColor, value: NSColor.systemRed, range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    @MainActor
    private func addDelimiter() {
        let str = NSMutableAttributedString(string: "\n─────────────────── restarted ──────────────────\n\n")
        // bold font
        str.addAttribute(.font, value: terminalFontBold, range: NSRange(location: 0, length: str.length))
        // secondary
        str.addAttribute(.foregroundColor, value: NSColor.secondaryLabelColor, range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    @MainActor
    func clear() {
        contents = NSMutableAttributedString()
        updateEvent.send()
    }

    func copyAll() {
        NSPasteboard.copy(contents.string)
    }
}

private class LineHeightDelegate: NSObject, NSLayoutManagerDelegate {
    private let fontLineHeight: CGFloat

    init(layoutManager: NSLayoutManager) {
        // cache this calculation for perf
        fontLineHeight = layoutManager.defaultLineHeight(for: terminalFont)
    }

    // this is the only good way to set line height.
    // paragraphStyle.lineHeightMultiple breaks incremental text search and adds all space to top of line
    // paragraphStyle.lineSpacing makes selection ugly (it's spacing *between* lines)
    // lineSpacingAfterGlyphAt causes visible line recycling on scroll (appearing/disappearing at top/bottom)
    // this method: search works, no ugly selection, centered spacing, no recycling
    // https://christiantietze.de/posts/2017/07/nstextview-proper-line-height/
    func layoutManager(
            _ layoutManager: NSLayoutManager,
            shouldSetLineFragmentRect lineFragmentRect: UnsafeMutablePointer<NSRect>,
            lineFragmentUsedRect: UnsafeMutablePointer<NSRect>,
            baselineOffset: UnsafeMutablePointer<CGFloat>,
            in textContainer: NSTextContainer,
            forGlyphRange glyphRange: NSRange) -> Bool {
        let lineHeight = fontLineHeight * terminalLineHeight
        let baselineNudge = (lineHeight - fontLineHeight)
                // The following factor is a result of experimentation:
                * 0.6

        var rect = lineFragmentRect.pointee
        rect.size.height = lineHeight

        var usedRect = lineFragmentUsedRect.pointee
        usedRect.size.height = max(lineHeight, usedRect.size.height) // keep emoji sizes

        lineFragmentRect.pointee = rect
        lineFragmentUsedRect.pointee = usedRect
        baselineOffset.pointee = baselineOffset.pointee + baselineNudge

        return true
    }

    // this works, but puts all padding at the bottom,
    // and causes visible lines appearing/disappearing at top/bottom when scrolling slowly
    /*
    func layoutManager(_ layoutManager: NSLayoutManager, lineSpacingAfterGlyphAt glyphIndex: Int,
                       withProposedLineFragmentRect rect: NSRect) -> CGFloat {
        5
    }
     */
}

private struct LogsTextView: NSViewRepresentable {
    let model: LogsViewModel

    class Coordinator {
        var cancellables = Set<AnyCancellable>()
        var layoutManagerDelegate: NSLayoutManagerDelegate?
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        let textView = scrollView.documentView as! NSTextView

        // textView.font and textView.isAutomaticLinkDetectionEnabled don't work
        if let layoutManager = textView.layoutManager {
            // keep strong ref (layoutManager.delegate = weak)
            context.coordinator.layoutManagerDelegate = LineHeightDelegate(layoutManager: layoutManager)
            layoutManager.delegate = context.coordinator.layoutManagerDelegate
        }
        textView.textContainerInset = NSSize(width: 8, height: 8)
        textView.isAutomaticDataDetectionEnabled = false
        textView.isIncrementalSearchingEnabled = true

        // char wrap, line height
        let paragraphStyle = NSMutableParagraphStyle.default.mutableCopy() as! NSMutableParagraphStyle
        paragraphStyle.lineBreakMode = .byCharWrapping
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
        // trigger initial update
        model.updateEvent.send()

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
    let model: LogsViewModel

    var body: some View {
        LogsTextView(model: model)
        .onAppear {
            model.start(isCompose: isCompose, args: args)
        }
        .onDisappear {
            model.stop()
        }
        .onChange(of: args) { newArgs in
            model.start(isCompose: isCompose, args: newArgs)
        }
    }
}

struct DockerLogsWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()
    @StateObject private var model = LogsViewModel()

    @State private var containerId: String?
    @State private var containerName: String? // saved once we get id
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
                .onAppear {
                    // save name so we can keep going after container is recreated
                    containerName = container.names.first
                }
            } else if let containerName,
                      let containers = vmModel.dockerContainers,
                      let container = containers.first(where: { $0.names.contains(containerName) }) {
                // if restarted, use name
                // don't update id - it'll cause unnecessary logs restart
                LogsView(isCompose: false,
                        args: ["logs", "-f", "-n", String(maxLines), container.id],
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

            vmModel.$dockerContainers.sink { containers in
                // if containers list changes,
                // and process has exited,
                // and (container ID && it's running) or (containerName && it's running) or (composeProject && any running)
                if model.process != nil {
                    return
                }
                guard let containers else {
                    return
                }

                if let containerId,
                   containers.contains(where: { $0.id == containerId && $0.running }) {
                    model.restart()
                } else if let containerName,
                          containers.contains(where: { $0.names.contains(containerName) && $0.running }) {
                    model.restart()
                } else if let composeProject,
                          containers.contains(where: {$0.labels[DockerLabels.composeProject] == composeProject && $0.running}) {
                    model.restart()
                }
            }.store(in: &model.cancellables)
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
