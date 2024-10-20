//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Foundation
import SwiftUI

private struct GeneratedDiagReport: Decodable {
    let zipPath: String
    let info: String
}

private enum DiagReporterState {
    case loading
    case error(String)
    case confirmation(GeneratedDiagReport)
    case uploading
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
            case let .confirmation(report):
                VStack(spacing: 16) {
                    Image(systemName: "questionmark.circle.fill")
                        .resizable()
                        .frame(width: 32, height: 32)

                    Text(
                        "A diagnostic report has been generated. You can review its contents before uploading it to our servers for review."
                    )
                    .multilineTextAlignment(.center)
                    .padding()

                    HStack {
                        Button(action: {
                            Task {
                                do {
                                    try await runProcessChecked(
                                        "/usr/bin/open",
                                        ["-b", "com.apple.archiveutility", report.zipPath])
                                } catch let processError as ProcessError {
                                    diagModel.state = .error(
                                        "(status \(processError.status)) \(processError.output)")
                                } catch {
                                    diagModel.state = .error(error.localizedDescription)
                                }
                            }
                        }) {
                            Text("Review")
                        }

                        Button(action: {
                            diagModel.state = .uploading

                            Task {
                                do {
                                    let output =
                                        (try await runProcessChecked(
                                            AppConfig.ctlExe,
                                            ["_internal", "upload-diag-report", report.zipPath]))
                                        .trimmingCharacters(in: .whitespacesAndNewlines)

                                    let reportSummary = report.info + "Full report: \(output)"

                                    if isBugReport {
                                        var urlComps = URLComponents(
                                            string: "https://orbstack.dev/issues/bug")!
                                        urlComps.queryItems = [
                                            URLQueryItem(name: "diag", value: reportSummary)
                                        ]
                                        NSWorkspace.shared.open(urlComps.url!)
                                        windowHolder.window?.close()
                                    } else {
                                        NSPasteboard.general.clearContents()
                                        NSPasteboard.general.setString(
                                            reportSummary,
                                            forType: NSPasteboard.PasteboardType.string)
                                        diagModel.state = .done
                                    }
                                } catch let processError as ProcessError {
                                    diagModel.state = .error(
                                        "(status \(processError.status)) \(processError.output)")
                                } catch {
                                    diagModel.state = .error(error.localizedDescription)
                                }
                            }
                        }) {
                            Text("Upload")
                        }
                    }

                }
            case .uploading:
                VStack(spacing: 16) {
                    ProgressView()
                    Text("Uploading report…")
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
                let output = try await runProcessChecked(
                    AppConfig.ctlExe, ["_internal", "generate-diag-report"])

                let decoder = JSONDecoder()
                decoder.keyDecodingStrategy = .convertFromSnakeCase
                let generated = try decoder.decode(
                    GeneratedDiagReport.self,
                    from: output.trimmingCharacters(in: .whitespacesAndNewlines).data(using: .utf8)!
                )

                diagModel.state = .confirmation(generated)
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
