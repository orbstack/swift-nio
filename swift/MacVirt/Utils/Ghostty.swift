import GhosttyKit
import Foundation

struct Ghostty {
    let app: ghostty_app_t

    init() {
        if ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) != GHOSTTY_SUCCESS {
            fatalError("Failed to initialize Ghostty")
        }

        let config = Config(theme: TerminalTheme.defaultDark)

        var runtime_cfg = ghostty_runtime_config_s(
                userdata: nil, 
                supports_selection_clipboard: false,
                wakeup_cb: {userdata in 
                    DispatchQueue.main.async {
                        ghostty_app_tick(AppDelegate.shared.ghostty.app)
                    }
                },
                action_cb: {_, _, _ in true},
                read_clipboard_cb: {_, _, _ in},
                confirm_read_clipboard_cb: {_, _, _, _ in},
                write_clipboard_cb: {_, _, _, _ in},
                close_surface_cb: {_, _ in}
            )

        self.app = ghostty_app_new(&runtime_cfg, config.ghostty_config)
    }
}

extension Ghostty {
    struct Config {
        var theme: TerminalTheme

        var ghostty_config: ghostty_config_t {
            var config_strings: [String] = []
            config_strings.append(contentsOf: theme.toGhosttyArgs())

            let config_strings_unsafe: UnsafeMutablePointer<UnsafeMutablePointer<CChar>?> = config_strings
            .map { strdup($0) }
            .withUnsafeBufferPointer { buffer in
                let ptr = UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>.allocate(capacity: buffer.count + 1)
                for (i, cstr) in buffer.enumerated() {
                    ptr[i] = cstr
                }
                ptr[buffer.count] = nil // Add terminating nullptr
                return ptr
            }

            let config = ghostty_config_new()!
            ghostty_config_load_strings(config, config_strings_unsafe, config_strings.count)
            ghostty_config_finalize(config)

            return config
        }

        init(theme: TerminalTheme) {
            self.theme = theme
        }

        func reload(app: ghostty_app_t) {
            ghostty_app_update_config(app, ghostty_config)
        }
    }
}