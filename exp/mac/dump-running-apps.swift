import AppKit

func dumpOne(app: NSRunningApplication) {
    print("app: \(app)")
    print("  bundleIdentifier: \(app.bundleIdentifier ?? "nil")")
    print("  bundleURL: \(app.bundleURL?.path ?? "nil")")
    print("  executableURL: \(app.executableURL?.path ?? "nil")")
    print("  localizedName: \(app.localizedName ?? "nil")")
    print("  processIdentifier: \(app.processIdentifier)")
    print("  isHidden: \(app.isHidden)")
    print("  isTerminated: \(app.isTerminated)")
    print("  isFinishedLaunching: \(app.isFinishedLaunching)")
    print("  ownsMenuBar: \(app.ownsMenuBar)")
    print("  activationPolicy: \(app.activationPolicy.rawValue)")
    print("  launchDate: \(app.launchDate?.description ?? "nil")")
    print("")
}
for app in NSWorkspace.shared.runningApplications {
    dumpOne(app: app)
}

print("\n\n\nLookup:")
for app in NSRunningApplication.runningApplications(withBundleIdentifier: "dev.kdrag0n.MacVirt") {
    dumpOne(app: app)
}
