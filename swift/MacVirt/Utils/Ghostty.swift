import AppKit
import Foundation
import GhosttyKit
import SwiftUI
import Defaults

class Ghostty {
    let app: ghostty_app_t
    var config: Config

    @MainActor
    init() {
        if ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) != GHOSTTY_SUCCESS {
            fatalError("Failed to initialize Ghostty")
        }

        config = Config()

        var runtime_cfg = ghostty_runtime_config_s(
            userdata: nil,
            supports_selection_clipboard: false,
            wakeup_cb: { userdata in
                DispatchQueue.main.async {
                    ghostty_app_tick(AppDelegate.shared.ghostty.app)
                }
            },
            action_cb: { _, _, _ in false },
            read_clipboard_cb: { _, _, _ in },
            confirm_read_clipboard_cb: { _, _, _, _ in },
            write_clipboard_cb: { _, _, _, _ in },
            close_surface_cb: { userdata, processAlive in
                NSLog("close_surface_cb: \(processAlive)")
                NotificationCenter.default.post(name: .ghosttyCloseSurface, object: Surface.surfaceUserdata(from: userdata))
            }
        )

        self.app = ghostty_app_new(&runtime_cfg, config.ghostty_config)
    }
}

extension Ghostty {
    static func ghosttyMods(_ flags: NSEvent.ModifierFlags) -> ghostty_input_mods_e {
        var mods: UInt32 = GHOSTTY_MODS_NONE.rawValue

        if flags.contains(.shift) { mods |= GHOSTTY_MODS_SHIFT.rawValue }
        if flags.contains(.control) { mods |= GHOSTTY_MODS_CTRL.rawValue }
        if flags.contains(.option) { mods |= GHOSTTY_MODS_ALT.rawValue }
        if flags.contains(.command) { mods |= GHOSTTY_MODS_SUPER.rawValue }
        if flags.contains(.capsLock) { mods |= GHOSTTY_MODS_CAPS.rawValue }

        // Handle sided input. We can't tell that both are pressed in the
        // Ghostty structure but thats okay -- we don't use that information.
        let rawFlags = flags.rawValue
        if rawFlags & UInt(NX_DEVICERSHIFTKEYMASK) != 0 {
            mods |= GHOSTTY_MODS_SHIFT_RIGHT.rawValue
        }
        if rawFlags & UInt(NX_DEVICERCTLKEYMASK) != 0 { mods |= GHOSTTY_MODS_CTRL_RIGHT.rawValue }
        if rawFlags & UInt(NX_DEVICERALTKEYMASK) != 0 { mods |= GHOSTTY_MODS_ALT_RIGHT.rawValue }
        if rawFlags & UInt(NX_DEVICERCMDKEYMASK) != 0 { mods |= GHOSTTY_MODS_SUPER_RIGHT.rawValue }

        return ghostty_input_mods_e(mods)
    }
    
    /// Returns the event modifier flags set for the Ghostty mods enum.
    static func eventModifierFlags(mods: ghostty_input_mods_e) -> NSEvent.ModifierFlags {
        var flags = NSEvent.ModifierFlags(rawValue: 0)
        if mods.rawValue & GHOSTTY_MODS_SHIFT.rawValue != 0 { flags.insert(.shift) }
        if mods.rawValue & GHOSTTY_MODS_CTRL.rawValue != 0 { flags.insert(.control) }
        if mods.rawValue & GHOSTTY_MODS_ALT.rawValue != 0 { flags.insert(.option) }
        if mods.rawValue & GHOSTTY_MODS_SUPER.rawValue != 0 { flags.insert(.command) }
        return flags
    }    
}

extension Ghostty {
    class Config {
        private var effectiveAppearance: NSAppearance
        private var colorScheme: ColorScheme {
            return effectiveAppearance.name.rawValue.lowercased().contains("dark") ? .dark : .light
        }

        var themePreference: TerminalThemePreference {
            return Defaults[.terminalTheme]
        }
        var theme: TerminalTheme {
            return TerminalTheme.forPreference(themePreference, colorScheme: colorScheme)
        }

        var ghostty_config: ghostty_config_t {
            var config_strings: [String] = []
            config_strings.append(contentsOf: theme.toGhosttyArgs())

            let config_strings_unsafe: UnsafeMutablePointer<UnsafeMutablePointer<CChar>?> =
                config_strings
                .map { strdup($0) }
                .withUnsafeBufferPointer { buffer in
                    let ptr = UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>.allocate(
                        capacity: buffer.count + 1)
                    for (i, cstr) in buffer.enumerated() {
                        ptr[i] = cstr
                    }
                    ptr[buffer.count] = nil  // Add terminating nullptr
                    return ptr
                }

            let config = ghostty_config_new()!
            ghostty_config_load_strings(config, config_strings_unsafe, config_strings.count)
            ghostty_config_finalize(config)

            return config
        }

        init() {
            self.effectiveAppearance = NSApplication.shared.effectiveAppearance

            Task {
                for await _ in Defaults.updates(.terminalTheme) {
                    reload()
                }
            }
        }

        func setAppearance(appearance: NSAppearance) {
            self.effectiveAppearance = appearance
            reload()
        }

        func reload() {
            DispatchQueue.main.async {
                ghostty_app_update_config(AppDelegate.shared.ghostty.app, self.ghostty_config)
            }
        }
    }
}

extension Ghostty {
    class Surface {
        let surface: ghostty_surface_t
        private var ghostty_size: ghostty_surface_size_s
        var size: CGSize {
            return CGSize(width: CGFloat(ghostty_size.width_px), height: CGFloat(ghostty_size.height_px))
        }

        init(app: ghostty_app_t, view: NSView, command: String, env: [String], size: CGSize) {
            let surface_config = Configuration(command: command, env: env)

            self.surface = surface_config.withCValue(view: view) { config in
                ghostty_surface_new(AppDelegate.shared.ghostty.app, &config)
            }

            ghostty_surface_set_size(surface, UInt32(size.width), UInt32(size.height))
            self.ghostty_size = ghostty_surface_size(surface)
        }

        convenience init(app: ghostty_app_t, view: NSView, command: String, env: [String]) {
            self.init(app: app, view: view, command: command, env: env, size: CGSize(width: 800, height: 600))
        }

        deinit {
            ghostty_surface_free(surface)
        }

        static func surfaceUserdata(from userdata: UnsafeMutableRawPointer?) -> NSView {
            return Unmanaged<NSView>.fromOpaque(userdata!).takeUnretainedValue()
        }

        func setSize(width: UInt32, height: UInt32) {
            ghostty_surface_set_size(surface, width, height)
            self.ghostty_size = ghostty_surface_size(surface)
        }

        func key(_ key_ev: ghostty_input_key_s) -> Bool {
            return ghostty_surface_key(surface, key_ev)
        }

        func sendText(_ text: String) {
            let len = text.utf8CString.count
            if (len == 0) { return }

            text.withCString { ptr in
                // len includes the null terminator so we do len - 1
                ghostty_surface_text(surface, ptr, UInt(len - 1))
            }
        }

        func selectedText() -> SelectedText {
            return SelectedText(surface: surface)
        }

        func quicklookFont() -> CTFont? {
            // Memory management here is wonky: ghostty_surface_quicklook_font
            // will create a copy of a CTFont, Swift will auto-retain the
            // unretained value passed into the dict, so we release the original.
            if let fontRaw = ghostty_surface_quicklook_font(surface) {
                let font = Unmanaged<CTFont>.fromOpaque(fontRaw)
                let ret = font.takeUnretainedValue()
                font.release()
                return ret
            }
            return nil
        }

        /// Returns the x/y coordinate of where the IME (Input Method Editor)
        /// keyboard should be rendered.
        func imePoint() -> (x: Double, y: Double) {
            var x: Double = 0
            var y: Double = 0
            ghostty_surface_ime_point(surface, &x, &y)
            return (x: x, y: y)
        }

        func preEdit(_ str: String?) {
            if let str {
                str.withCString { ptr in
                    ghostty_surface_preedit(surface, ptr, UInt(str.utf8CString.count - 1))
                }
            } else {
                ghostty_surface_preedit(surface, nil, 0)
            }
        }

        func sendMouseScroll(_ scrollEvent: MouseScrollEvent) {
            ghostty_surface_mouse_scroll(surface, scrollEvent.x, scrollEvent.y, scrollEvent.mods.cScrollMods)
        }

        func sendMousePos(_ posEvent: MousePosEvent) {
            ghostty_surface_mouse_pos(surface, posEvent.x, posEvent.y, posEvent.mods.cMods) 
        }

        func sendMouseButton(_ buttonEvent: MouseButtonEvent) {
            ghostty_surface_mouse_button(surface, 
            ghostty_input_mouse_state_e(buttonEvent.action.rawValue),
            ghostty_input_mouse_button_e(buttonEvent.button.rawValue),
            buttonEvent.mods.cMods)
        }
        
        func sendMouseButton(_ button: MouseButton, _ action: MouseAction, _ mods: InputMods) {
            sendMouseButton(MouseButtonEvent(action: action, button: button, mods: mods))
        }
    }
}

extension Ghostty.Surface {
    class SelectedText {
        private var text: ghostty_text_s
        private var surface: ghostty_surface_t

        init(surface: ghostty_surface_t) {
            self.surface = surface
            self.text = ghostty_text_s()
            ghostty_surface_read_selection(surface, &text)
        }

        deinit {
            ghostty_surface_free_text(surface, &text)
        }

        func range() -> NSRange {
            return NSRange(location: Int(text.offset_start), length: Int(text.offset_len))
        }

        func string() -> String {
            return String(cString: text.text)
        }

        func topLeftCoords() -> (x: Double, y: Double) {
            return (x: text.tl_px_x, y: text.tl_px_y)
        }
    }

    /// `ghostty_input_mouse_momentum_e` - Momentum phase for scroll events
    enum Momentum: UInt8, CaseIterable {
        case none = 0
        case began = 1
        case stationary = 2
        case changed = 3
        case ended = 4
        case cancelled = 5
        case mayBegin = 6
        
        var cMomentum: ghostty_input_mouse_momentum_e {
            switch self {
            case .none: GHOSTTY_MOUSE_MOMENTUM_NONE
            case .began: GHOSTTY_MOUSE_MOMENTUM_BEGAN
            case .stationary: GHOSTTY_MOUSE_MOMENTUM_STATIONARY
            case .changed: GHOSTTY_MOUSE_MOMENTUM_CHANGED
            case .ended: GHOSTTY_MOUSE_MOMENTUM_ENDED
            case .cancelled: GHOSTTY_MOUSE_MOMENTUM_CANCELLED
            case .mayBegin: GHOSTTY_MOUSE_MOMENTUM_MAY_BEGIN
            }
        }

        init(_ phase: NSEvent.Phase) {
        switch phase {
        case .began: self = .began
        case .stationary: self = .stationary
        case .changed: self = .changed
        case .ended: self = .ended
        case .cancelled: self = .cancelled
        case .mayBegin: self = .mayBegin
        default: self = .none
        }
    }
    }

    struct ScrollMods {
        let rawValue: Int32
        
        /// True if this is a high-precision scroll event (e.g., trackpad, Magic Mouse)
        var precision: Bool {
            rawValue & 0b0000_0001 != 0
        }
        
        /// The momentum phase of the scroll event for inertial scrolling
        var momentum: Momentum {
            let momentumBits = (rawValue >> 1) & 0b0000_0111
            return Momentum(rawValue: UInt8(momentumBits)) ?? .none
        }
        
        init(precision: Bool = false, momentum: Momentum = .none) {
            var value: Int32 = 0
            if precision {
                value |= 0b0000_0001
            }
            value |= Int32(momentum.rawValue) << 1
            self.rawValue = value
        }
        
        init(rawValue: Int32) {
            self.rawValue = rawValue
        }
        
        var cScrollMods: ghostty_input_scroll_mods_t {
            rawValue
        }
    }

    struct MouseScrollEvent {
        let x: Double
        let y: Double
        let mods: ScrollMods

        init(
            x: Double,
            y: Double,
            mods: ScrollMods = .init(rawValue: 0)
        ) {
            self.x = x
            self.y = y
            self.mods = mods
        }
    }

    struct InputMods: OptionSet {
        let rawValue: UInt32
        
        static let none = InputMods(rawValue: GHOSTTY_MODS_NONE.rawValue)
        static let shift = InputMods(rawValue: GHOSTTY_MODS_SHIFT.rawValue)
        static let ctrl = InputMods(rawValue: GHOSTTY_MODS_CTRL.rawValue)
        static let alt = InputMods(rawValue: GHOSTTY_MODS_ALT.rawValue)
        static let `super` = InputMods(rawValue: GHOSTTY_MODS_SUPER.rawValue)
        static let caps = InputMods(rawValue: GHOSTTY_MODS_CAPS.rawValue)
        static let shiftRight = InputMods(rawValue: GHOSTTY_MODS_SHIFT_RIGHT.rawValue)
        static let ctrlRight = InputMods(rawValue: GHOSTTY_MODS_CTRL_RIGHT.rawValue)
        static let altRight = InputMods(rawValue: GHOSTTY_MODS_ALT_RIGHT.rawValue)
        static let superRight = InputMods(rawValue: GHOSTTY_MODS_SUPER_RIGHT.rawValue)
        
        var cMods: ghostty_input_mods_e {
            ghostty_input_mods_e(rawValue)
        }
        
        init(rawValue: UInt32) {
            self.rawValue = rawValue
        }
        
        init(cMods: ghostty_input_mods_e) {
            self.rawValue = cMods.rawValue
        }
        
        init(nsFlags: NSEvent.ModifierFlags) {
            self.init(cMods: Ghostty.ghosttyMods(nsFlags))
        }
        
        var nsFlags: NSEvent.ModifierFlags {
            Ghostty.eventModifierFlags(mods: cMods)
        }
    }

    struct MousePosEvent {
        let x: Double
        let y: Double
        let mods: InputMods 
    }

    struct MouseButton: OptionSet {
        let rawValue: UInt32
        
        static let left = MouseButton(rawValue: GHOSTTY_MOUSE_LEFT.rawValue)
        static let right = MouseButton(rawValue: GHOSTTY_MOUSE_RIGHT.rawValue)
        static let middle = MouseButton(rawValue: GHOSTTY_MOUSE_MIDDLE.rawValue)
    }

    struct MouseAction: OptionSet {
        let rawValue: UInt32
        
        static let pressed = MouseAction(rawValue: GHOSTTY_MOUSE_PRESS.rawValue)
        static let released = MouseAction(rawValue: GHOSTTY_MOUSE_RELEASE.rawValue)
    }

    struct MouseButtonEvent {
        let action: MouseAction
        let button: MouseButton
        let mods: InputMods
    }

    /// The configuration for a surface. For any configuration not set, defaults will be chosen from
    // /// libghostty, usually from the Ghostty configuration.
    struct Configuration {
        /// Explicit font size to use in points
        var fontSize: Float32? = nil

        /// Explicit working directory to set
        var workingDirectory: String? = nil

        /// Explicit command to set
        var command: String? = nil

        /// Environment variables to set for the terminal
        var environmentVariables: [String: String] = [:]

        /// Extra input to send as stdin
        var initialInput: String? = nil

        init(command: String, env: [String]) {
            self.command = command
            for envvar in env {
                let parts = envvar.split(separator: "=")
                self.environmentVariables[String(parts[0])] = String(parts[1])
            }
        }

        /// Provides a C-compatible ghostty configuration within a closure. The configuration
        /// and all its string pointers are only valid within the closure.
        func withCValue<T>(
            view: NSView, _ body: (inout ghostty_surface_config_s) throws -> T
        ) rethrows -> T {
            var config = ghostty_surface_config_new()
            config.userdata = Unmanaged.passUnretained(view).toOpaque()
            config.platform_tag = GHOSTTY_PLATFORM_MACOS
            config.platform = ghostty_platform_u(
            macos: ghostty_platform_macos_s(
                nsview: Unmanaged.passUnretained(view).toOpaque()
            ))
            config.scale_factor = NSScreen.main!.backingScaleFactor

            // Zero is our default value that means to inherit the font size.
            config.font_size = fontSize ?? 0

            // Use withCString to ensure strings remain valid for the duration of the closure
            return try workingDirectory.withCString { cWorkingDir in
                config.working_directory = cWorkingDir

                return try command.withCString { cCommand in
                    config.command = cCommand

                    return try initialInput.withCString { cInput in
                        config.initial_input = cInput

                        // Convert dictionary to arrays for easier processing
                        let keys = Array(environmentVariables.keys)
                        let values = Array(environmentVariables.values)

                        // Create C strings for all keys and values
                        return try keys.withCStrings { keyCStrings in
                            return try values.withCStrings { valueCStrings in
                                // Create array of ghostty_env_var_s
                                var envVars = [ghostty_env_var_s]()
                                envVars.reserveCapacity(environmentVariables.count)
                                for i in 0..<environmentVariables.count {
                                    envVars.append(
                                        ghostty_env_var_s(
                                            key: keyCStrings[i],
                                            value: valueCStrings[i]
                                        ))
                                }

                                return try envVars.withUnsafeMutableBufferPointer { buffer in
                                    config.env_vars = buffer.baseAddress
                                    config.env_var_count = environmentVariables.count
                                    return try body(&config)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

extension Notification.Name {
    static let ghosttyCloseSurface = Notification.Name("dev.orbstack.macvirt.ghostty.closeSurface")
}