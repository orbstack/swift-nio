//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Foundation
import SwiftUI

private enum GenericCommandState {
    case loading
    case error(String)
    case done
}

private class GenericCommandViewModel: ObservableObject {
    @Published var state: GenericCommandState = .loading
}

struct ResetDataView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var model = GenericCommandViewModel()
    @StateObject private var windowHolder = WindowHolder()

    @State private var presentConfirm = true

    var body: some View {
        VStack {
            switch model.state {
            case .loading:
                VStack(spacing: 16) {
                    ProgressView()
                    Text("Resetting data…")
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
                    Text("Data reset.")
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
        .akAlert(isPresented: $presentConfirm, style: .critical) {
            "Reset all data?"
            "All containers, images, volumes, Kubernetes resources, and Linux machines will be permanently lost."

            AKAlertButton("Reset", destructive: true) {
                Task {
                    do {
                        try await runProcessChecked(AppConfig.ctlExe, ["reset", "-y"])
                        model.state = .done
                    } catch let processError as ProcessError {
                        model.state = .error(
                            "(status \(processError.status)) \(processError.stderr)")
                        return
                    } catch {
                        model.state = .error(error.localizedDescription)
                        return
                    }

                    // done! now restart vmgr
                    await vmModel.tryStartDaemon()
                }
            }

            AKAlertButton("Cancel") {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                    windowHolder.window?.close()
                }
            }
        }
        .background(VisualEffectView().ignoresSafeArea())
        .windowRestorability(false)
        .windowHolder(windowHolder)
    }
}
