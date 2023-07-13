//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

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

    var body: some View {
        VStack {
            switch diagModel.state {
            case .loading:
                VStack(spacing: 16) {
                    ProgressView()
                    Text("Generating report…")
                }
            case .error(let message):
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
                try await runProcessChecked(AppConfig.ctlExe, ["report"])
                diagModel.state = .done
            } catch let processError as ProcessError {
                diagModel.state = .error("(status \(processError.status))\n\(processError.output)")
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
