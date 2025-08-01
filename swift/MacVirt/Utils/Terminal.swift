//
// Created by Danny Lin on 5/7/23.
//

import Carbon
import Combine
import Defaults
import Foundation
import GhosttyKit
import SwiftUI

struct TerminalTabView: View {
    let executable: String
    let args: [String]
    let env: [String]

    init(executable: String, args: [String], env: [String] = []) {
        self.executable = executable
        self.args = args
        self.env = env
    }

    var body: some View {
        GeometryReader { geometry in
            TerminalTabNSViewRepresentable(
                executable: executable, args: args, env: env, size: geometry.size)
        }
    }
}

struct TerminalTabNSViewRepresentable: NSViewRepresentable {
    let executable: String
    let args: [String]
    let env: [String]

    let size: CGSize

    func makeNSView(context: Context) -> TerminalTabNSView {
        return TerminalTabNSView(executable: executable, args: args, env: env)
    }

    func updateNSView(_ nsView: TerminalTabNSView, context: Context) {
        nsView.sizeDidChange(size)
    }
}

class TerminalTabNSView: NSView {
    @Environment(\.colorScheme) var colorScheme
    @Default(.terminalTheme) var terminalTheme

    let executable: String
    let args: [String]
    let env: [String]

    override var acceptsFirstResponder: Bool {
        return true
    }

    var surface: ghostty_surface_t?
    var surfaceSize: ghostty_surface_size_s?

    init(executable: String, args: [String], env: [String] = []) {
        self.executable = executable
        self.args = args
        self.env = env

        super.init(frame: NSMakeRect(0, 0, 800, 600))

        let surface_config = SurfaceConfiguration()
        surface = surface_config.withCValue(view: self) { config in
            ghostty_surface_new(AppDelegate.shared.ghostty, &config)
        }
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func sizeDidChange(_ size: CGSize) {
        // Ghostty wants to know the actual framebuffer size... It is very important
        // here that we use "size" and NOT the view frame. If we're in the middle of
        // an animation (i.e. a fullscreen animation), the frame will not yet be updated.
        // The size represents our final size we're going for.
        let scaledSize = self.convertToBacking(size)
        setSurfaceSize(width: UInt32(scaledSize.width), height: UInt32(scaledSize.height))
    }

    private func setSurfaceSize(width: UInt32, height: UInt32) {
        guard let surface = surface else { return }

        NSLog("setting surface size to \(width)x\(height)")

        // Update our core surface
        ghostty_surface_set_size(surface, width, height)

        // Update our cached size metrics
        let size = ghostty_surface_size(surface)
        DispatchQueue.main.async {
            // DispatchQueue required since this may be called by SwiftUI off
            // the main thread and Published changes need to be on the main
            // thread. This caused a crash on macOS <= 14.
            self.surfaceSize = size
        }
    }

    override func keyUp(with event: NSEvent) {
        _ = keyAction(GHOSTTY_ACTION_RELEASE, event: event)
    }

    // ---- ---- ---- ---- ---- ---- ----- ---- ----- ---- -----
    // ---- ---- ---- START GHOSTTY PASTE-DUMPING ---- ---- ----
    // ---- ---- ---- ---- ---- ---- ----- ---- ----- ---- -----

    private func keyAction(
        _ action: ghostty_input_action_e,
        event: NSEvent,
        translationEvent: NSEvent? = nil,
        text: String? = nil,
        composing: Bool = false
    ) -> Bool {
        NSLog("keyAction: \(action) \(event.characters ?? "nil")")

        guard let surface = self.surface else { return false }

        NSLog("keyAction: \(action) \(event.characters ?? "nil")")

        var key_ev = ghosttyKeyEvent(
            event, action, translationMods: translationEvent?.modifierFlags)
        key_ev.composing = composing

        // For text, we only encode UTF8 if we don't have a single control
        // character. Control characters are encoded by Ghostty itself.
        // Without this, `ctrl+enter` does the wrong thing.
        if let text, text.count > 0,
            let codepoint = text.utf8.first, codepoint >= 0x20
        {
            return text.withCString { ptr in
                key_ev.text = ptr
                return ghostty_surface_key(surface, key_ev)
            }
        } else {
            return ghostty_surface_key(surface, key_ev)
        }
    }

    func ghosttyKeyEvent(
        _ event: NSEvent,
        _ action: ghostty_input_action_e,
        translationMods: NSEvent.ModifierFlags? = nil
    ) -> ghostty_input_key_s {
        var key_ev: ghostty_input_key_s = .init()
        key_ev.action = action
        key_ev.keycode = UInt32(event.keyCode)

        // We can't infer or set these safely from this method. Since text is
        // a cString, we can't use self.characters because of garbage collection.
        // We have to let the caller handle this.
        key_ev.text = nil
        key_ev.composing = false

        // macOS provides no easy way to determine the consumed modifiers for
        // producing text. We apply a simple heuristic here that has worked for years
        // so far: control and command never contribute to the translation of text,
        // assume everything else did.
        key_ev.mods = ghosttyMods(event.modifierFlags)
        key_ev.consumed_mods = ghosttyMods(
            (translationMods ?? event.modifierFlags)
                .subtracting([.control, .command]))

        // Our unshifted codepoint is the codepoint with no modifiers. We
        // ignore multi-codepoint values. We have to use `byApplyingModifiers`
        // instead of `charactersIgnoringModifiers` because the latter changes
        // behavior with ctrl pressed and we don't want any of that.
        key_ev.unshifted_codepoint = 0
        if event.type == .keyDown || event.type == .keyUp {
            if let chars = event.characters(byApplyingModifiers: []),
                let codepoint = chars.unicodeScalars.first
            {
                key_ev.unshifted_codepoint = codepoint.value
            }
        }

        return key_ev
    }

    var keyTextAccumulator: [String]? = nil
    private var markedText: NSMutableAttributedString = NSMutableAttributedString()

    /// Records the timestamp of the last event to performKeyEquivalent that we need to save.
    /// We currently save all commands with command or control set.
    ///
    /// For command+key inputs, the AppKit input stack calls performKeyEquivalent to give us a chance
    /// to handle them first. If we return "false" then it goes through the standard AppKit responder chain.
    /// For an NSTextInputClient, that may redirect some commands _before_ our keyDown gets called.
    /// Concretely: Command+Period will do: performKeyEquivalent, doCommand ("cancel:"). In doCommand,
    /// we need to know that we actually want to handle that in keyDown, so we send it back through the
    /// event dispatch system and use this timestamp as an identity to know to actually send it to keyDown.
    ///
    /// Why not send it to keyDown always? Because if the user rebinds a command to something we
    /// actually handle then we do want the standard response chain to handle the key input. Unfortunately,
    /// we can't know what a command is bound to at a system level until we let it flow through the system.
    /// That's the crux of the problem.
    ///
    /// So, we have to send it back through if we didn't handle it.
    ///
    /// The next part of the problem is comparing NSEvent identity seems pretty nasty. I couldn't
    /// find a good way to do it. I originally stored a weak ref and did identity comparison but that
    /// doesn't work and for reasons I couldn't figure out the value gets mangled (fields don't match
    /// before/after the assignment). I suspect it has something to do with the fact an NSEvent is wrapping
    /// a lower level event pointer and its just not surviving the Swift runtime somehow. I don't know.
    ///
    /// The best thing I could find was to store the event timestamp which has decent granularity
    /// and compare that. To further complicate things, some events are synthetic and have a zero
    /// timestamp so we have to protect against that. Fun!
    var lastPerformKeyEvent: TimeInterval?

    override func keyDown(with event: NSEvent) {
        NSLog("keydown: \(event.characters ?? "nil")")

        guard let surface = self.surface else {
            self.interpretKeyEvents([event])
            NSLog("surface is nil")
            return
        }

        // On any keyDown event we unset our bell state
        // bell = false

        // We need to translate the mods (maybe) to handle configs such as option-as-alt
        let translationModsGhostty = eventModifierFlags(
            mods: ghostty_surface_key_translation_mods(
                surface,
                ghosttyMods(event.modifierFlags)
            )
        )

        // There are hidden bits set in our event that matter for certain dead keys
        // so we can't use translationModsGhostty directly. Instead, we just check
        // for exact states and set them.
        var translationMods = event.modifierFlags
        for flag in [NSEvent.ModifierFlags.shift, .control, .option, .command] {
            if translationModsGhostty.contains(flag) {
                translationMods.insert(flag)
            } else {
                translationMods.remove(flag)
            }
        }

        // If the translation modifiers are not equal to our original modifiers
        // then we need to construct a new NSEvent. If they are equal we reuse the
        // old one. IMPORTANT: we MUST reuse the old event if they're equal because
        // this keeps things like Korean input working. There must be some object
        // equality happening in AppKit somewhere because this is required.
        let translationEvent: NSEvent
        if translationMods == event.modifierFlags {
            translationEvent = event
        } else {
            translationEvent =
                NSEvent.keyEvent(
                    with: event.type,
                    location: event.locationInWindow,
                    modifierFlags: translationMods,
                    timestamp: event.timestamp,
                    windowNumber: event.windowNumber,
                    context: nil,
                    characters: event.characters(byApplyingModifiers: translationMods) ?? "",
                    charactersIgnoringModifiers: event.charactersIgnoringModifiers ?? "",
                    isARepeat: event.isARepeat,
                    keyCode: event.keyCode
                ) ?? event
        }

        let action = event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS

        // By setting this to non-nil, we note that we're in a keyDown event. From here,
        // we call interpretKeyEvents so that we can handle complex input such as Korean
        // language.
        keyTextAccumulator = []
        defer { keyTextAccumulator = nil }

        // We need to know what the length of marked text was before this event to
        // know if these events cleared it.
        let markedTextBefore = markedText.length > 0

        // We need to know the keyboard layout before below because some keyboard
        // input events will change our keyboard layout and we don't want those
        // going to the terminal.
        let keyboardIdBefore: String? =
            if !markedTextBefore {
                KeyboardLayout.id
            } else {
                nil
            }

        // If we are in a keyDown then we don't need to redispatch a command-modded
        // key event (see docs for this field) so reset this to nil because
        // `interpretKeyEvents` may dispach it.
        self.lastPerformKeyEvent = nil

        self.interpretKeyEvents([translationEvent])

        // If our keyboard changed from this we just assume an input method
        // grabbed it and do nothing.
        if !markedTextBefore && keyboardIdBefore != KeyboardLayout.id {
            return
        }

        // If we have marked text, we're in a preedit state. The order we
        // do this and the key event callbacks below doesn't matter since
        // we control the preedit state only through the preedit API.
        syncPreedit(clearIfNeeded: markedTextBefore)
        
        NSLog("keydown about to send")

        if let list = keyTextAccumulator, list.count > 0 {
            // If we have text, then we've composed a character, send that down.
            // These never have "composing" set to true because these are the
            // result of a composition.
            for text in list {
                _ = keyAction(
                    action,
                    event: event,
                    translationEvent: translationEvent,
                    text: text
                )
            }
        } else {
            // We have no accumulated text so this is a normal key event.
            _ = keyAction(
                action,
                event: event,
                translationEvent: translationEvent,
                text: ghosttyCharacters(event: translationEvent),

                // We're composing if we have preedit (the obvious case). But we're also
                // composing if we don't have preedit and we had marked text before,
                // because this input probably just reset the preedit state. It shouldn't
                // be encoded. Example: Japanese begin composing, the press backspace.
                // This should only cancel the composing state but not actually delete
                // the prior input characters (prior to the composing).
                composing: markedText.length > 0 || markedTextBefore
            )
        }
    }
    



        /// Sync the preedit state based on the markedText value to libghostty
    private func syncPreedit(clearIfNeeded: Bool = true) {
        guard let surface else { return }

        if markedText.length > 0 {
            let str = markedText.string
            let len = str.utf8CString.count
            if len > 0 {
                markedText.string.withCString { ptr in
                    // Subtract 1 for the null terminator
                    ghostty_surface_preedit(surface, ptr, UInt(len - 1))
                }
            }
        } else if clearIfNeeded {
            // If we had marked text before but don't now, we're no longer
            // in a preedit state so we can clear it.
            ghostty_surface_preedit(surface, nil, 0)
        }
    }

    /// Returns the event modifier flags set for the Ghostty mods enum.
    func eventModifierFlags(mods: ghostty_input_mods_e) -> NSEvent.ModifierFlags {
        var flags = NSEvent.ModifierFlags(rawValue: 0)
        if mods.rawValue & GHOSTTY_MODS_SHIFT.rawValue != 0 { flags.insert(.shift) }
        if mods.rawValue & GHOSTTY_MODS_CTRL.rawValue != 0 { flags.insert(.control) }
        if mods.rawValue & GHOSTTY_MODS_ALT.rawValue != 0 { flags.insert(.option) }
        if mods.rawValue & GHOSTTY_MODS_SUPER.rawValue != 0 { flags.insert(.command) }
        return flags
    }

    func ghosttyMods(_ flags: NSEvent.ModifierFlags) -> ghostty_input_mods_e {
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

}

class KeyboardLayout {
    /// Return a string ID of the current keyboard input source.
    static var id: String? {
        if let source = TISCopyCurrentKeyboardInputSource()?.takeRetainedValue(),
            let sourceIdPointer = TISGetInputSourceProperty(source, kTISPropertyInputSourceID)
        {
            let sourceId = unsafeBitCast(sourceIdPointer, to: CFString.self)
            return sourceId as String
        }

        return nil
    }
}

    func ghosttyCharacters(event: NSEvent) -> String? {
        // If we have no characters associated with this event we do nothing.
        guard let characters = event.characters else { return nil }

        if characters.count == 1,
           let scalar = characters.unicodeScalars.first {
            // If we have a single control character, then we return the characters
            // without control pressed. We do this because we handle control character
            // encoding directly within Ghostty's KeyEncoder.
            if scalar.value < 0x20 {
                return event.characters(byApplyingModifiers: event.modifierFlags.subtracting(.control))
            }

            // If we have a single value in the PUA, then it's a function key and
            // we don't want to send PUA ranges down to Ghostty.
            if scalar.value >= 0xF700 && scalar.value <= 0xF8FF {
                return nil
            }
        }

        return characters
    }

extension Array where Element == String {
    /// Executes a closure with an array of C string pointers.
    func withCStrings<T>(_ body: ([UnsafePointer<Int8>?]) throws -> T) rethrows -> T {
        // Handle empty array
        if isEmpty {
            return try body([])
        }

        // Recursive helper to process strings
        func helper(
            index: Int, accumulated: [UnsafePointer<Int8>?],
            body: ([UnsafePointer<Int8>?]) throws -> T
        ) rethrows -> T {
            if index == count {
                return try body(accumulated)
            }

            return try self[index].withCString { cStr in
                var newAccumulated = accumulated
                newAccumulated.append(cStr)
                return try helper(index: index + 1, accumulated: newAccumulated, body: body)
            }
        }

        return try helper(index: 0, accumulated: [], body: body)
    }
}

extension Optional where Wrapped == String {
    /// Executes a closure with a C string pointer, handling nil gracefully.
    func withCString<T>(_ body: (UnsafePointer<Int8>?) throws -> T) rethrows -> T {
        if let string = self {
            return try string.withCString(body)
        } else {
            return try body(nil)
        }
    }
}

/// The configuration for a surface. For any configuration not set, defaults will be chosen from
/// libghostty, usually from the Ghostty configuration.
struct SurfaceConfiguration {
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

    init() {}

    init(from config: ghostty_surface_config_s) {
        self.fontSize = config.font_size
        if let workingDirectory = config.working_directory {
            self.workingDirectory = String.init(cString: workingDirectory, encoding: .utf8)
        }
        if let command = config.command {
            self.command = String.init(cString: command, encoding: .utf8)
        }

        // Convert the C env vars to Swift dictionary
        if config.env_var_count > 0, let envVars = config.env_vars {
            for i in 0..<config.env_var_count {
                let envVar = envVars[i]
                if let key = String(cString: envVar.key, encoding: .utf8),
                    let value = String(cString: envVar.value, encoding: .utf8)
                {
                    self.environmentVariables[key] = value
                }
            }
        }
    }

    /// Provides a C-compatible ghostty configuration within a closure. The configuration
    /// and all its string pointers are only valid within the closure.
    func withCValue<T>(
        view: TerminalTabNSView, _ body: (inout ghostty_surface_config_s) throws -> T
    ) rethrows -> T {
        var config = ghostty_surface_config_new()
        config.userdata = Unmanaged.passUnretained(view).toOpaque()
        #if os(macOS)
            config.platform_tag = GHOSTTY_PLATFORM_MACOS
            config.platform = ghostty_platform_u(
                macos: ghostty_platform_macos_s(
                    nsview: Unmanaged.passUnretained(view).toOpaque()
                ))
            config.scale_factor = NSScreen.main!.backingScaleFactor
        #elseif os(iOS)
            config.platform_tag = GHOSTTY_PLATFORM_IOS
            config.platform = ghostty_platform_u(
                ios: ghostty_platform_ios_s(
                    uiview: Unmanaged.passUnretained(view).toOpaque()
                ))
            // Note that UIScreen.main is deprecated and we're supposed to get the
            // screen through the view hierarchy instead. This means that we should
            // probably set this to some default, then modify the scale factor through
            // libghostty APIs when a UIView is attached to a window/scene. TODO.
            config.scale_factor = UIScreen.main.scale
        #else
            #error("unsupported target")
        #endif

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
