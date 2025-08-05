import AppKit
import Foundation
import GhosttyKit
import SwiftUI

class Ghostty {
    let app: ghostty_app_t
    var config: Config

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
            action_cb: { _, _, _ in true },
            read_clipboard_cb: { _, _, _ in },
            confirm_read_clipboard_cb: { _, _, _, _ in },
            write_clipboard_cb: { _, _, _, _ in },
            close_surface_cb: { _, _ in }
        )

        self.app = ghostty_app_new(&runtime_cfg, config.ghostty_config)
    }
}

extension Ghostty {
    class Config {
        private var effectiveAppearance: NSAppearance
        private var colorScheme: ColorScheme {
            return effectiveAppearance.name.rawValue.lowercased().contains("dark") ? .dark : .light
        }

        var themePreference: TerminalThemePreference = .def
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

        init(app: ghostty_app_t, view: NSView, executable: String, args: [String], env: [String], size: CGSize) {
            let surface_config = SurfaceConfiguration(executable: executable, args: args, env: env)

            self.surface = surface_config.withCValue(view: view) { config in
                ghostty_surface_new(AppDelegate.shared.ghostty.app, &config)
            }

            ghostty_surface_set_size(surface, UInt32(size.width), UInt32(size.height))
            self.ghostty_size = ghostty_surface_size(surface)
        }

        convenience init(app: ghostty_app_t, view: NSView, executable: String, args: [String], env: [String]) {
            self.init(app: app, view: view, executable: executable, args: args, env: env, size: CGSize(width: 800, height: 600))
        }

        deinit {
            ghostty_surface_free(surface)
        }

        func setSize(width: UInt32, height: UInt32) {
            ghostty_surface_set_size(surface, width, height)
            self.ghostty_size = ghostty_surface_size(surface)
        }

        func key(_ key_ev: ghostty_input_key_s) -> Bool {
            return ghostty_surface_key(surface, key_ev)
        }
    }

    /// The configuration for a surface. For any configuration not set, defaults will be chosen from
    // /// libghostty, usually from the Ghostty configuration.
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

        init(executable: String, args: [String], env: [String]) {
            self.command = executable + " " + args.joined(separator: " ")
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
