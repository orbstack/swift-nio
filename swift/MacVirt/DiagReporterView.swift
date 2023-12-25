//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Foundation
import SwiftUI

private enum DiagReporterState {
    case loading
    case error(String)
    case done
}

private class DiagReporterViewModel: ObservableObject {
    @Published var state: DiagReporterState = .loading
}

struct DiagReporterView: View {
    @StateObject private var diagModel = DiagReporterViewModel()
    @StateObject private var windowHolder = WindowHolder()

    let isBugReport: Bool

    var body: some View {
        VStack {
            switch diagModel.state {
            case .loading:
                VStack(spacing: 16) {
                    ProgressView()
                    Text("Generating report…")
                }
            case let .error(message):
                VStack(spacing: 16) {
                    Image(systemName: "exclamationmark.circle.fill")
                        .resizable()
                        .frame(width: 32, height: 32)
                        .foregroundColor(.red)
                    Text(message)
                }
            case .done:
                VStack(spacing: 16) {
                    Image(systemName: "checkmark.circle.fill")
                        .resizable()
                        .frame(width: 32, height: 32)
                        .foregroundColor(.green)
                    Text("Copied")
                        .fontWeight(.medium)
                }
                .onAppear {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        windowHolder.window?.close()
                    }
                }
            }
        }
        .frame(width: 300, height: 300)
        .task {
            do {
                // quiet mode: don't copy to clipboard or print extra stuff
                let output = try await runProcessChecked(AppConfig.ctlExe, isBugReport ? ["report", "-q"] : ["report"])
                if isBugReport {
                    // open bug report and close immediately
                    var urlComps = URLComponents(string: "https://orbstack.dev/issues/bug")!
                    urlComps.queryItems = [URLQueryItem(name: "diag", value: output)]
                    NSWorkspace.shared.open(urlComps.url!)

                    windowHolder.window?.close()
                } else {
                    // show success and close later
                    diagModel.state = .done
                }
            } catch let processError as ProcessError {
                diagModel.state = .error("(status \(processError.status)) \(processError.output)")
            } catch {
                diagModel.state = .error(error.localizedDescription)
            }
        }
        .background(VisualEffectView().ignoresSafeArea())
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
    }
}
