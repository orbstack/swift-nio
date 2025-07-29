//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Defaults
import Foundation
import SwiftUI

private let maxLines = 5000
private let maxChars = maxLines * 150  // avg line len - easier to do it like this
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

                // do not append \n
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

private struct LogLine: Identifiable {
    let id: UUID
    let text: NSAttributedString
}

private class LogsViewModel: ObservableObject {
    enum EditAction {
        case append
        case appendAndTruncate
        case clear
    }

    var lines = [LogLine]()
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
            if !isFirstStart {
                addDelimiter()
            }
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
    private func add(attributedString: NSAttributedString) {
        let line = LogLine(id: UUID(), text: attributedString)
        lines.append(line)
        // truncate if needed
        if lines.count > maxLines {
            lines.removeFirst()
            updateEvent.send(.appendAndTruncate)
        } else {
            updateEvent.send(.append)
        }
    }

    @MainActor
    private func add(error: String) {
        let str = NSMutableAttributedString(string: error)
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
        lines = []
        updateEvent.send(.clear)
    }

    func copyAll() {
        NSPasteboard.copy(lines.map { $0.text.string }.joined(separator: "\n"))
    }
}

private struct LogsTableView: NSViewRepresentable {
    let model: LogsViewModel
    let commandModel: CommandViewModel

    class Coordinator: NSObject, NSTableViewDelegate, NSTableViewDataSource {
        let model: LogsViewModel
        var cancellables = Set<AnyCancellable>()

        init(model: LogsViewModel) {
            self.model = model
        }

        func numberOfRows(in tableView: NSTableView) -> Int {
            model.lines.count
        }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int)
            -> NSView?
        {
            if row >= model.lines.count {
                return nil
            }

            let textView = NSTextField(labelWithAttributedString: model.lines[row].text)
            textView.translatesAutoresizingMaskIntoConstraints = false
            textView.usesSingleLineMode = true

            let cellView = NSTableCellView()
            cellView.textField = textView
            cellView.addSubview(textView)
            NSLayoutConstraint.activate([
                textView.leadingAnchor.constraint(equalTo: cellView.leadingAnchor),
                textView.trailingAnchor.constraint(equalTo: cellView.trailingAnchor),
                textView.centerYAnchor.constraint(equalTo: cellView.centerYAnchor),
                textView.heightAnchor.constraint(equalToConstant: 16),
            ])

            return cellView
        }
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSScrollView()
        scrollView.hasHorizontalScroller = true
        scrollView.findBarPosition = .aboveContent

        let tableView = NSTableView()
        scrollView.documentView = tableView

        tableView.delegate = context.coordinator
        tableView.dataSource = context.coordinator

        tableView.allowsMultipleSelection = true
        tableView.headerView = nil
        tableView.usesAlternatingRowBackgroundColors = false
        tableView.columnAutoresizingStyle = .noColumnAutoresizing  // Prevent auto-resizing for better horizontal scrolling
        tableView.rowHeight = 20  // Set fixed row height for consistent vertical centering

        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("column"))
        column.minWidth = 400  // Set a reasonable minimum width
        column.width = 800  // Set initial width
        column.maxWidth = 10000  // Allow very wide columns for long log lines
        tableView.addTableColumn(column)

        let debouncedScrollToEnd = Debouncer(delay: 0.05) {
            tableView.scrollRowToVisible(tableView.numberOfRows - 1)
        }

        model.updateEvent
            .receive(on: DispatchQueue.main)
            .sink { [weak tableView] editAction in
                guard let tableView else { return }

                switch editAction {
                case .append:
                    tableView.beginUpdates()
                    tableView.insertRows(at: IndexSet(integer: tableView.numberOfRows))
                    tableView.endUpdates()
                    debouncedScrollToEnd.call()
                case .appendAndTruncate:
                    tableView.beginUpdates()
                    tableView.removeRows(at: IndexSet(integer: 0))
                    tableView.insertRows(at: IndexSet(integer: tableView.numberOfRows))
                    tableView.endUpdates()
                    debouncedScrollToEnd.call()
                case .clear:
                    tableView.beginUpdates()
                    tableView.removeRows(at: IndexSet(integersIn: 0..<tableView.numberOfRows))
                    tableView.endUpdates()
                }
            }.store(in: &context.coordinator.cancellables)

        // commandModel.searchCommand.sink { [weak tableView] _ in
        //     guard let tableView else { return }
        //     // need .tag holder
        //     let button = NSButton()
        //     button.tag = NSTextFinder.Action.showFindInterface.rawValue
        //     tableView.performFindPanelAction(button)
        // }.store(in: &context.coordinator.cancellables)

        DispatchQueue.main.async {
            tableView.window?.makeFirstResponder(tableView)
        }

        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(model: model)
    }
}

private struct LogsView: View {
    @EnvironmentObject private var commandModel: CommandViewModel

    let cmdExe: String
    let args: [String]
    let extraArgs: [String]
    let extraState: [String]
    let model: LogsViewModel

    var body: some View {
        LogsTableView(model: model, commandModel: commandModel)
            .onAppear {
                model.start(cmdExe: cmdExe, args: args + extraArgs)
            }
            .onDisappear {
                model.stop()
            }
            .onChange(of: args) { _, newArgs in
                model.start(cmdExe: cmdExe, args: newArgs + extraArgs)
            }
            .onChange(of: extraArgs) { _, newExtraArgs in
                model.start(cmdExe: cmdExe, args: args + newExtraArgs, clearAndRestart: true)
            }
            .onChange(of: extraState) { _, _ in
                model.start(cmdExe: cmdExe, args: args + extraArgs, clearAndRestart: true)
            }
    }
}

struct DockerLogsContentView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var commandModel: CommandViewModel
    @EnvironmentObject private var windowTracker: WindowTracker

    @StateObject private var model = LogsViewModel()

    let cid: DockerContainerId
    // individual container, not compose
    let standalone: Bool
    let extraComposeArgs: [String]
    let allDisabled: Bool

    init(
        cid: DockerContainerId, standalone: Bool, extraComposeArgs: [String] = [],
        allDisabled: Bool = false
    ) {
        self.cid = cid
        self.standalone = standalone
        self.extraComposeArgs = extraComposeArgs
        self.allDisabled = allDisabled
    }

    var body: some View {
        DockerStateWrapperView(\.dockerContainers) { containers, _ in
            if allDisabled {
                ContentUnavailableViewCompat("No Containers Selected", systemImage: "moon.zzz")
            } else if case let .container(containerId) = cid,
                let container = containers.byId[containerId]
            {
                LogsView(
                    cmdExe: AppConfig.dockerExe,
                    args: ["logs", "-f", "-n", String(maxLines), containerId],
                    extraArgs: [],
                    // trigger restart on start/stop state change
                    // don't trigger on starting/stopping/deleting/...
                    extraState: [container.state == "running" ? "running" : "not_running"],
                    model: model
                )
                .if(standalone) {
                    $0
                        .navigationTitle(container.userName)
                        .navigationSubtitle(WindowTitles.containerLogsBase)
                }
                .onAppear {
                    // save name so we can keep going after container is recreated
                    model.lastContainerName = container.names.first
                }
                .onReceive(vmModel.toolbarActionRouter) { action in
                    if action == .dockerOpenContainerInNewWindow {
                        container.showLogs(windowTracker: windowTracker)
                    }
                }
            } else if let containerName = model.lastContainerName,
                let container = vmModel.dockerContainers?.byName[containerName]
            {
                // if restarted, use name
                // don't update id - it'll cause unnecessary logs restart
                LogsView(
                    cmdExe: AppConfig.dockerExe,
                    args: ["logs", "-f", "-n", String(maxLines), container.id],
                    extraArgs: [],
                    extraState: [],
                    model: model
                )
                .if(standalone) {
                    $0
                        .navigationTitle(container.userName)
                        .navigationSubtitle(WindowTitles.containerLogsBase)
                }
            } else if case let .compose(composeProject) = cid {
                LogsView(
                    cmdExe: AppConfig.dockerComposeExe,
                    args: ["-p", composeProject, "logs", "-f", "-n", String(maxLines)],
                    extraArgs: extraComposeArgs,
                    extraState: [],
                    model: model)
            } else {
                ContentUnavailableViewCompat(
                    "Container Removed", systemImage: "trash", desc: "No logs available.")
            }
        }
        .onAppear {
            // TODO: why doesn't for-await + .task() work? (that way we get auto-cancel)
            model.monitorCommands(commandModel: commandModel)
            model.monitorContainers(vmModel: vmModel, cid: cid)
        }
        .frame(minWidth: 400, minHeight: 200)
    }
}

struct DockerLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @StateObject private var commandModel = CommandViewModel()

    @SceneStorage("DockerLogs_containerId") private var containerId: String?

    var body: some View {
        Group {
            if let containerId {
                DockerLogsContentView(cid: .container(id: containerId), standalone: true)
                    .onAppear {
                        windowTracker.openDockerLogWindowIds.insert(.container(id: containerId))
                    }
                    .onDisappear {
                        windowTracker.openDockerLogWindowIds.remove(.container(id: containerId))
                    }
            }
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            containerId = url.lastPathComponent
        }
        .toolbar(forCommands: commandModel)
    }
}

struct DockerComposeLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var commandModel = CommandViewModel()

    // for hide sidebar workaround - unused
    @State private var collapsed = false
    // mirror from SceneStorage to fix flicker
    @State private var selection = "all"
    @State private var disabledChildren = Set<String>()
    @State private var isHoveringSection = false

    @SceneStorage("DockerComposeLogs_composeProject") private var composeProject: String?
    @SceneStorage("DockerComposeLogs_selection") private var savedSelection = "all"

    var body: some View {
        let children =
            vmModel.dockerContainers?.byComposeProject[composeProject]?.sorted {
                $0.userName < $1.userName
            } ?? []

        NavigationView {
            List {
                let selBinding = Binding<String?>(
                    get: {
                        selection
                    },
                    set: {
                        if let sel = $0 {
                            selection = sel
                        }
                    })

                if let composeProject {
                    let projectLogArgs =
                        disabledChildren.isEmpty
                        ? []
                        :  // all
                        children
                            .map { $0.userName }
                            .filter { !disabledChildren.contains($0) }
                    let allDisabled = disabledChildren.count == children.count && !children.isEmpty

                    NavigationLink(tag: "all", selection: selBinding) {
                        DockerLogsContentView(
                            cid: .compose(project: composeProject),
                            standalone: false, extraComposeArgs: projectLogArgs,
                            allDisabled: allDisabled)
                    } label: {
                        Label("All", systemImage: "square.stack.3d.up")
                    }
                    .onAppear {
                        windowTracker.openDockerLogWindowIds.insert(
                            .compose(project: composeProject))
                    }
                    .onDisappear {
                        windowTracker.openDockerLogWindowIds.remove(
                            .compose(project: composeProject))
                    }

                    let bindings = children.map { container in
                        let serviceName = container.userName
                        return Binding<Bool>(
                            get: {
                                !disabledChildren.contains(serviceName)
                            },
                            set: {
                                if $0 {
                                    disabledChildren.remove(serviceName)
                                } else {
                                    disabledChildren.insert(serviceName)
                                }
                            })
                    }
                    Section {
                        ForEach(children, id: \.id) { container in
                            NavigationLink(tag: "container:\(container.id)", selection: selBinding)
                            {
                                DockerLogsContentView(cid: container.cid, standalone: false)
                            } label: {
                                let serviceName = container.userName
                                let enabledBinding = Binding<Bool>(
                                    get: {
                                        !disabledChildren.contains(serviceName)
                                    },
                                    set: {
                                        if $0 {
                                            disabledChildren.remove(serviceName)
                                        } else {
                                            disabledChildren.insert(serviceName)
                                        }
                                    })

                                HStack {
                                    Label {
                                        Text(serviceName)
                                    } icon: {
                                        // icon = red/green status dot
                                        Image(nsImage: SystemImages.statusDot(container.statusDot))
                                    }

                                    Spacer()

                                    Toggle(isOn: enabledBinding) {
                                        Text("Show in All")
                                    }
                                    .labelsHidden()
                                    .toggleStyle(.checkbox)
                                    .help("Show in All")
                                }
                                .contextMenu {
                                    Toggle(isOn: enabledBinding) {
                                        Text("Show in All")
                                    }
                                }
                            }
                        }
                    } header: {
                        HStack {
                            Text("Services")

                            Spacer()

                            // crude aproximation of macOS 15 .sectionActions
                            Toggle(sources: bindings, isOn: \.self) {
                                Text("Show All")
                            }
                            .toggleStyle(.checkbox)
                            .labelsHidden()
                            .help("Show All")
                            // lines up with checkboxes
                            .padding(.trailing, 14)
                            .opacity(isHoveringSection ? 1 : 0)
                        }
                        .frame(height: 28)
                    }
                    // no point in collapsing: you can just collapse the sidebar if you intend on only seeing All.
                    // arrow causes checkbox to shift around so this makes it easier
                    .collapsible(false)
                }
            }
            .onHover {
                isHoveringSection = $0
            }
            .listStyle(.sidebar)
            .background(SplitViewAccessor(sideCollapsed: $collapsed))

            ContentUnavailableViewCompat(
                "No Service Selected", systemImage: "questionmark.app.fill")
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            // check "base64" query param
            // for backward compat with restored state URLs, this is query-gated
            if url.query?.contains("base64") == true,
                let decoded = Data(base64URLEncoded: url.lastPathComponent)
            {
                composeProject = String(data: decoded, encoding: .utf8)
            } else {
                composeProject = url.lastPathComponent
            }
        }
        .onAppear {
            selection = savedSelection
        }
        .onChange(of: selection) { _, newSelection in
            savedSelection = newSelection
        }
        .if(composeProject != nil) {
            $0.navigationTitle(composeProject!)
        }
        .navigationSubtitle(WindowTitles.projectLogsBase)
        .toolbar(forCommands: commandModel, hasSidebar: true)
    }
}

extension View {
    fileprivate func toolbar(forCommands commandModel: CommandViewModel, hasSidebar: Bool = false)
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

// TODO: move to K8s/
struct K8SLogsContentView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var commandModel: CommandViewModel
    @StateObject private var model = LogsViewModel()

    let kid: K8SResourceId
    let containerName: String?

    var body: some View {
        K8SStateWrapperView(\.k8sPods) { pods, _ in
            if case let .pod(namespace, name) = kid,
                pods.contains(where: { $0.id == kid })
            {
                LogsView(
                    cmdExe: AppConfig.kubectlExe,
                    args: [
                        "logs", "--context", K8sConstants.context, "-n", namespace, "pod/\(name)",
                        "-f",
                    ],
                    extraArgs: containerName == nil
                        ? ["--all-containers=true"] : ["-c", containerName!],
                    extraState: [],
                    model: model
                )
                .navigationTitle(name)
                .navigationSubtitle(WindowTitles.podLogsBase)
            } else {
                ContentUnavailableViewCompat(
                    "Pod Removed", systemImage: "trash", desc: "No logs available.")
            }
        }
        .onAppear {
            // TODO: why doesn't for-await + .task() work? (that way we get auto-cancel)
            model.monitorCommands(commandModel: commandModel)
            // TODO: equivalent of monitorContainers for pod recreate? or unlikely b/c of deployment + random names
        }
        .frame(minWidth: 400, minHeight: 200)
    }
}

struct K8SPodLogsWindow: View {
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var commandModel = CommandViewModel()

    // for hide sidebar workaround - unused
    @State private var collapsed = false
    // mirror from SceneStorage to fix flicker
    @State private var selection = "all"

    @SceneStorage("K8SLogs_namespaceAndName") private var namespaceAndName: String?
    @SceneStorage("K8SLogs_selection") private var savedSelection = "all"

    var body: some View {
        NavigationView {
            List {
                let selBinding = Binding<String?>(
                    get: {
                        selection
                    },
                    set: {
                        if let sel = $0 {
                            selection = sel
                        }
                    })

                if let namespaceAndName,
                    let kid = K8SResourceId.podFromNamespaceAndName(namespaceAndName)
                {
                    let children =
                        vmModel.k8sPods?.first { $0.id == kid }?.status.containerStatuses?.sorted {
                            ($0.name ?? "") < ($1.name ?? "")
                        } ?? []

                    NavigationLink(tag: "all", selection: selBinding) {
                        K8SLogsContentView(kid: kid, containerName: nil)
                    } label: {
                        Label("All", systemImage: "square.stack.3d.up")
                    }
                    .onAppear {
                        windowTracker.openK8sLogWindowIds.insert(kid)
                    }
                    .onDisappear {
                        windowTracker.openK8sLogWindowIds.remove(kid)
                    }

                    Section("Containers") {
                        ForEach(children, id: \.name) { container in
                            let k8sContainerName = container.name ?? "<unknown>"
                            NavigationLink(
                                tag: "container:\(k8sContainerName)", selection: selBinding
                            ) {
                                K8SLogsContentView(kid: kid, containerName: k8sContainerName)
                            } label: {
                                Label {
                                    Text(k8sContainerName)
                                } icon: {
                                    // icon = red/green status dot
                                    Image(
                                        nsImage: SystemImages.statusDot(
                                            isRunning: container.ready ?? false))
                                }
                            }
                        }
                    }
                }
            }
            .listStyle(.sidebar)
            .background(SplitViewAccessor(sideCollapsed: $collapsed))

            ContentUnavailableViewCompat(
                "No Service Selected", systemImage: "questionmark.app.fill")
        }
        .environmentObject(commandModel)
        .onOpenURL { url in
            if let decoded = Data(base64URLEncoded: url.lastPathComponent),
                let namespaceAndName = String(data: decoded, encoding: .utf8)
            {
                self.namespaceAndName = namespaceAndName
            }
        }
        .onAppear {
            selection = savedSelection
        }
        .onChange(of: selection) { _, newSelection in
            savedSelection = newSelection
        }
        .toolbar(forCommands: commandModel, hasSidebar: true)
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
