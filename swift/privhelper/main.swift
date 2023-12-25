import Foundation
import SecureXPC

if CommandLine.arguments.count > 1 {
    // CLI
    var arguments = CommandLine.arguments
    _ = arguments.removeFirst()
    NSLog("args: \(arguments)")

    if let firstArgument = arguments.first {
        if firstArgument == Uninstaller.cliCommand {
            try Uninstaller.uninstallFromCli(withArguments: arguments)
        } else {
            NSLog("argument not recognized: \(firstArgument)")
        }
    }
} else if getppid() == 1 {
    // server
    NSLog("starting server")

    let server = try XPCServer.forMachService()
    server.registerRoute(PHShared.symlinkRoute, handler: HelperServer.symlink)
    server.registerRoute(PHShared.uninstallRoute, handler: Uninstaller.uninstallFromXPC)
    server.registerRoute(PHShared.updateRoute, handler: Updater.updateHelperTool)
    server.setErrorHandler { error in
        if case .connectionInvalid = error {
            // ignore: client disconnected
        } else {
            NSLog("error: \(error)")
        }
    }
    activityTracker.kick()
    server.startAndBlock()
} else {
    print("Usage: \(CommandLine.arguments[0]) \(Uninstaller.cliCommand)")
}
