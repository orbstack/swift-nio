//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Defaults
import Foundation
import SwiftUI

let logsMaxLines = 5000
private let maxChars = logsMaxLines * 150  // avg line len - easier to do it like this
private let bottomScrollThreshold = 256.0
private let fontSize = 12.5
private let terminalLineHeight = 1.2

private let terminalFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
private let terminalFontBold = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
private let terminalColor = NSColor.textColor

private let urlRegex = try! NSRegularExpression(
    pattern:
        #"http(s)?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{2,256}(\.[a-z]{2,6})?\b([-a-zA-Z0-9@:%_\+.~#?&\/\/=]*)"#
)
private let ansiColorRegex = try! NSRegularExpression(pattern: #"\u001B\[([0-9]{1,2};?)*?m"#)
private let unsupportedAnsiRegex = try! NSRegularExpression(
    pattern: #"\u001B\[(?:[=?].+[a-zA-Z]|\d+[a-zA-Z])"#)

private let ansiColorPalette: [NSColor] = [
    // keep in mind that ansi colors are meant for white-on-black
    .textBackgroundColor,  // black
    .systemRed,
    .systemGreen,
    .systemOrange,  // systemYellow has bad contrast in light
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor,  // white

    // bright colors
    .systemGray,  // "bright black" is used for dim text: echo -e '\e[90m2023-09-01T00:48:52.163\e[0m Starting'
    // TODO: blend 0.4 with textColor, for light and dark
    .systemRed,
    .systemGreen,
    .systemOrange,
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor,  // white
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

private class PtyPipe: Pipe, @unchecked Sendable {
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
            if ch == 10 || ch == 13 {  // \n or \r
                if lastCh == 13 {
                    // skip \n after \r
                    lastCh = ch
                    continue
                }

                buf.append(10)  // \n
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

class CommandViewModel: ObservableObject {
    let searchCommand = PassthroughSubject<Void, Never>()
    let clearCommand = PassthroughSubject<Void, Never>()
    let copyAllCommand = PassthroughSubject<Void, Never>()
}

class LogsViewModel: ObservableObject {

    enum EditAction {
        case append(NSAttributedString)
        case replace(range: NSRange, replacementString: NSAttributedString)
        case clear
    }

    let contents = NSMutableAttributedString()
    let updateEvent = PassthroughSubject<EditAction, Never>()

    var process: Process?
    private var lastAnsiState = AnsiState()
    private var isFirstStart = true

    private var lastCmdExe: String?
    private var lastArgs: [String]?
    private var lastLineDate: Date?

    private var cancellables: Set<AnyCancellable> = []
    @Published var lastContainerName: String?  // saved once we get id

    @MainActor
    func monitorContainers(vmModel: VmViewModel, cid: DockerContainerId) {
        vmModel.$dockerContainers.sink { [weak self] containers in
            guard let self else { return }

            // if containers list changes,
            // and process has exited,
            // and (container ID && it's running) or (containerName && it's running) or (composeProject && any running)
            if self.process != nil {
                return
            }
            guard let containers else {
                return
            }

            if case let .container(containerId) = cid,
                let container = containers.byId[containerId],
                container.running
            {
                self.restart()
            } else if let lastContainerName,
                let container = containers.byName[lastContainerName],
                container.running
            {
                self.restart()
            } else if case let .compose(composeProject) = cid,
                let children = containers.byComposeProject[composeProject],
                children.contains(where: { $0.running })
            {
                self.restart()
            }
        }.store(in: &cancellables)
    }

    @MainActor
    func monitorCommands(commandModel: CommandViewModel) {
        commandModel.clearCommand.sink { [weak self] in
            self?.clear()
        }.store(in: &cancellables)

        commandModel.copyAllCommand.sink { [weak self] in
            self?.copyAll()
        }.store(in: &cancellables)

        // search command is monitored by GUI
    }

    @MainActor
    func start(cmdExe: String, args: [String], clearAndRestart: Bool = false) {
        // reset first
        stop()

        NSLog("Starting log stream: cmdExe=\(cmdExe), args=\(args)")
        lastAnsiState = AnsiState()
        lastCmdExe = cmdExe
        lastArgs = args
        let args = args
        if clearAndRestart {
            // clear for compose checkbox disabledChildren change
            clear()
        } else {
            // append arg to filter since last received line, for restart
            // we don't do fancy restart continuation logic anymore:
            // it's not necessary, as the CLI will replay a reasonable amount of logs
            // this causes a lot of issues that aren't worth fixing
            /*
            if let lastLineDate {
                let formatter = ISO8601DateFormatter()
                formatter.formatOptions.insert(.withFractionalSeconds)
                // for k8s this is --since-time
                if cmdExe == AppConfig.kubectlExe {
                    args.append("--since-time=\(formatter.string(from: lastLineDate))")
                } else {
                    args.append("--since=\(formatter.string(from: lastLineDate))")
                }
            }
            */

            // always clear
            clear()

            // if not first start, add delimiter
            // if !isFirstStart {
            //     addDelimiter()
            // }
        }

        isFirstStart = false

        let task = Process()
        task.launchPath = cmdExe
        // force: we do existing-data check in GUI
        task.arguments = args

        // env is more robust, user can mess with context
        var newEnv = ProcessInfo.processInfo.environment
        newEnv["TERM"] = "xterm"  // 16 color only
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
                lastLineDate = Date()  // for restart
                add(terminalLine: line)
            }
        }

        task.terminationHandler = { process in
            let status = process.terminationStatus
            let reason = process.terminationReason

            // mark as exited for restarting on container state change
            if self.process?.processIdentifier == process.processIdentifier {
                self.process = nil
            }

            DispatchQueue.main.async { [weak self] in
                guard let self else { return }

                reader.finish()
                // ignore our own SIGKILL
                if status != 0 && reason != .uncaughtSignal {
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
        NSLog("Ending log stream: cmdExe=\(lastCmdExe ?? ""), args=\(lastArgs ?? [])")

        if let process {
            // .terminate sends SIGTERM
            kill(process.processIdentifier, SIGKILL)
        }
        process = nil

        // don't restart
        lastCmdExe = nil
        lastArgs = nil
    }

    @MainActor
    func restart() {
        NSLog("Restarting log stream: cmdExe=\(lastCmdExe ?? ""), args=\(lastArgs ?? [])")
        if let lastCmdExe, let lastArgs {
            start(cmdExe: lastCmdExe, args: lastArgs)
        }
    }

    @MainActor
    private func add(terminalLine: String) {
        let attributedStr = NSMutableAttributedString(string: terminalLine)
        // font
        attributedStr.addAttribute(
            .font, value: terminalFont, range: NSRange(location: 0, length: attributedStr.length))
        // color
        attributedStr.addAttribute(
            .foregroundColor, value: terminalColor,
            range: NSRange(location: 0, length: attributedStr.length))

        // parse links first, before indexes change
        var matches = urlRegex.matches(
            in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
        for match in matches {
            let url = (terminalLine as NSString).substring(with: match.range)
            attributedStr.addAttribute(.link, value: url, range: match.range)
        }

        // parse colors from ANSI escapes - state machine
        matches = ansiColorRegex.matches(
            in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
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
                    state.colorFg = 8 + code - 90
                    state.bold = true
                case 100...107:
                    state.colorBg = 8 + code - 100
                    state.bold = true
                default:
                    continue
                }
            }

            // state updated. add last mark
            if state != lastAnsiState {
                lastAnsiState.addAttribute(
                    to: attributedStr,
                    range: NSRange(location: lastI, length: match.range.location - lastI))
                lastAnsiState = state
                lastI = match.range.location
            }
        }
        // add terminating mark
        state.addAttribute(
            to: attributedStr,
            range: NSRange(location: lastI, length: terminalLine.utf16.count - lastI))
        lastAnsiState = state
        // then delete escapes
        for match in matches.reversed() {
            attributedStr.deleteCharacters(in: match.range)
        }

        // delete unsupported escapes (like ESC[?25l used by nextjs, and ESC[4D [move cursor by columns])
        let newStr = attributedStr.string
        matches = unsupportedAnsiRegex.matches(
            in: newStr, range: NSRange(location: 0, length: newStr.utf16.count))
        for match in matches.reversed() {
            attributedStr.deleteCharacters(in: match.range)
        }

        add(attributedString: attributedStr)
    }

    @MainActor
    private func add(attributedString: NSMutableAttributedString) {
        contents.append(attributedString)
        // publish
        updateEvent.send(.append(attributedString))
        // truncate if needed
        if contents.length > maxChars {
            let truncateRange = NSRange(location: 0, length: contents.length - maxChars)
            contents.deleteCharacters(in: truncateRange)
            updateEvent.send(
                .replace(range: truncateRange, replacementString: NSAttributedString()))
        }
    }

    @MainActor
    private func add(error: String) {
        let str = NSMutableAttributedString(string: error + "\n")
        // bold font
        str.addAttribute(
            .font, value: terminalFontBold, range: NSRange(location: 0, length: str.length))
        // red
        str.addAttribute(
            .foregroundColor, value: NSColor.systemRed,
            range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    @MainActor
    private func addDelimiter() {
        let str = NSMutableAttributedString(
            string: "\n─────────────────── restarted ──────────────────\n\n")
        // font (bold causes overlapping box chars)
        str.addAttribute(
            .font, value: terminalFont, range: NSRange(location: 0, length: str.length))
        // secondary gray (secondaryLabelColor also causes overlap bleed)
        str.addAttribute(
            .foregroundColor, value: NSColor.systemGray,
            range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    @MainActor
    func clear() {
        contents.setAttributedString(NSAttributedString())
        updateEvent.send(.clear)
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
        _: NSLayoutManager,
        shouldSetLineFragmentRect lineFragmentRect: UnsafeMutablePointer<NSRect>,
        lineFragmentUsedRect: UnsafeMutablePointer<NSRect>,
        baselineOffset: UnsafeMutablePointer<CGFloat>,
        in _: NSTextContainer,
        forGlyphRange _: NSRange
    ) -> Bool {
        let lineHeight = fontLineHeight * terminalLineHeight
        let baselineNudge =
            (lineHeight - fontLineHeight)
            // The following factor is a result of experimentation:
            * 0.6

        var rect = lineFragmentRect.pointee
        rect.size.height = lineHeight

        var usedRect = lineFragmentUsedRect.pointee
        usedRect.size.height = max(lineHeight, usedRect.size.height)  // keep emoji sizes

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
    let commandModel: CommandViewModel
    let wordWrap: Bool

    class Coordinator {
        var cancellables = Set<AnyCancellable>()
        var layoutManagerDelegate: NSLayoutManagerDelegate?
        var lastWordWrap = true
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        let textView = scrollView.documentView as! NSTextView

        // enable horizontal scroll for non-wrapped case
        textView.isHorizontallyResizable = true
        scrollView.hasHorizontalScroller = true
        textView.maxSize = CGSize(
            width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)

        // textView.font and textView.isAutomaticLinkDetectionEnabled don't work
        if let layoutManager = textView.layoutManager {
            // keep strong ref (layoutManager.delegate = weak)
            context.coordinator.layoutManagerDelegate = LineHeightDelegate(
                layoutManager: layoutManager)
            layoutManager.delegate = context.coordinator.layoutManagerDelegate
        }
        textView.textContainerInset = NSSize(width: 8, height: 8)
        textView.isAutomaticDataDetectionEnabled = false
        textView.isIncrementalSearchingEnabled = true

        // char wrap, line height
        let paragraphStyle =
            NSMutableParagraphStyle.default.mutableCopy() as! NSMutableParagraphStyle
        paragraphStyle.lineBreakMode = .byCharWrapping
        textView.defaultParagraphStyle = paragraphStyle

        textView.isEditable = false
        textView.usesFindBar = true

        let debouncedScrollToEnd = Debouncer(delay: 0.05) {
            if let clipView = textView.enclosingScrollView?.contentView,
                let layoutManager = textView.layoutManager,
                let textContainer = textView.textContainer
            {
                layoutManager.ensureLayout(for: textContainer)
                let userRect = layoutManager.usedRect(for: textContainer)
                clipView.bounds.origin = CGPoint(
                    x: textView.textContainerInset.width / 2, y: userRect.maxY)
            }
        }

        model.updateEvent
            .receive(on: DispatchQueue.main)
            .sink { [weak textView] editAction in
                guard let textView else { return }

                switch editAction {
                case .append(let string):
                    textView.textStorage?.append(string)

                    if let clipView = textView.enclosingScrollView?.contentView {
                        let shouldScroll = (clipView.bounds.maxY >= textView.bounds.maxY - 1)
                        if shouldScroll {
                            debouncedScrollToEnd.call()
                        }
                    }
                case .replace(let range, let replacementString):
                    textView.textStorage?.replaceCharacters(in: range, with: replacementString)
                case .clear:
                    textView.string = ""
                }
            }.store(in: &context.coordinator.cancellables)

        commandModel.searchCommand.sink { [weak textView] _ in
            guard let textView else { return }
            // need .tag holder
            let button = NSButton()
            button.tag = NSTextFinder.Action.showFindInterface.rawValue
            textView.performFindPanelAction(button)
        }.store(in: &context.coordinator.cancellables)

        DispatchQueue.main.async {
            textView.window?.makeFirstResponder(textView)
        }

        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
        if wordWrap != context.coordinator.lastWordWrap {
            setWordWrap(scrollView: nsView, wrap: wordWrap)
            context.coordinator.lastWordWrap = wordWrap
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    func setWordWrap(scrollView: NSScrollView, wrap: Bool) {
        let textView = scrollView.documentView as! NSTextView

        if wrap {
            let sz = scrollView.contentSize
            textView.frame = CGRect(x: 0, y: 0, width: sz.width, height: 0)
            textView.textContainer?.containerSize = CGSize(
                width: sz.width, height: CGFloat.greatestFiniteMagnitude)
            textView.textContainer?.widthTracksTextView = true
        } else {
            textView.textContainer?.widthTracksTextView = false
            textView.textContainer?.containerSize = CGSize(
                width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        }

        // otherwise it scrolls to top when re-enabling word wrap
        textView.scrollToEndOfDocument(nil)
    }
}

struct LogsView: View {
    @EnvironmentObject private var commandModel: CommandViewModel

    @Default(.logsWordWrap) private var wordWrap

    let cmdExe: String
    let args: [String]
    let extraArgs: [String]
    let extraState: [String]
    let model: LogsViewModel

    var body: some View {
        LogsTextView(model: model, commandModel: commandModel, wordWrap: wordWrap)
            .onAppear {
                model.start(cmdExe: cmdExe, args: args + extraArgs)
            }
            .onDisappear {
                model.stop()
            }
            .onChange(of: args) { newArgs in
                model.start(cmdExe: cmdExe, args: newArgs + extraArgs)
            }
            .onChange(of: extraArgs) { newExtraArgs in
                model.start(cmdExe: cmdExe, args: args + newExtraArgs, clearAndRestart: true)
            }
            .onChange(of: extraState) { _ in
                model.start(cmdExe: cmdExe, args: args + extraArgs, clearAndRestart: true)
            }
    }
}

extension View {
    func toolbar(forCommands commandModel: CommandViewModel, hasSidebar: Bool = false)
        -> some View
    {
        toolbar {
            ToolbarItem(placement: .navigation) {
                // unlike main window, we never use NavigationSplitView b/c sidebar button bug
                // only show sidebar
                if hasSidebar {
                    ToggleSidebarButton()
                }
            }

            ToolbarItem(placement: .automatic) {
                Button {
                    commandModel.copyAllCommand.send()
                } label: {
                    Label("Copy", systemImage: "doc.on.doc")
                }
                .help("Copy")
                .keyboardShortcut("c", modifiers: [.command, .shift])
            }

            ToolbarItem(placement: .automatic) {
                Button {
                    commandModel.clearCommand.send()
                } label: {
                    Label("Clear", systemImage: "trash")
                }
                .help("Clear")
                .keyboardShortcut("k", modifiers: [.command])
            }

            ToolbarItem(placement: .automatic) {
                Button {
                    commandModel.searchCommand.send()
                } label: {
                    Label("Search", systemImage: "magnifyingglass")
                }
                .help("Search")
            }
        }
    }
}

private class Debouncer {
    private var timer: Timer?
    private let delay: TimeInterval
    private var handler: () -> Void

    init(delay: TimeInterval, handler: @escaping () -> Void) {
        self.delay = delay
        self.handler = handler
    }

    func call() {
        if timer == nil {
            self.handler()
        }

        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: delay, repeats: false) { [weak self] _ in
            self?.handler()
        }
    }
}
