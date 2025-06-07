//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Defaults
import Foundation
import SwiftUI

private enum AuthState {
    case loading
    case error(String)
    case done
}

private class AuthViewModel: ObservableObject {
    @Published var state: AuthState = .loading
}

struct AuthView: View {
    @EnvironmentObject var vmModel: VmViewModel
    @StateObject private var model = AuthViewModel()
    @StateObject private var windowHolder = WindowHolder()

    let sheetPresented: Binding<Bool>?

    var body: some View {
        VStack {
            Spacer()

            switch model.state {
            case .loading:
                VStack(spacing: 16) {
                    ProgressView()
                    Text("Continue in your browser…")
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
                    Text("Signed in")
                        .fontWeight(.medium)
                }
                .onAppear {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        if isSheet {
                            sheetPresented?.wrappedValue = false
                        } else {
                            windowHolder.window?.close()
                        }
                    }
                }
            }

            Spacer()

            // TODO: use .overlay to avoid shifting layout up
            if isSheet {
                HStack {
                    Button("Cancel", role: .cancel) {
                        sheetPresented?.wrappedValue = false
                    }
                    .controlSize(.large)

                    Spacer()
                }
            }
        }
        .padding(16)
        .frame(width: 300, height: 300)
        .task {
            do {
                // GUI will never trigger reauth if not needed (e.g. for switch org)
                var args = ["login", "-f"]
                if let ssoDomain = Defaults[.mdmSsoDomain], !ssoDomain.isEmpty {
                    args.append("--domain")
                    args.append(ssoDomain)
                }

                // quiet mode: don't copy to clipboard or print extra stuff
                try await runProcessChecked(AppConfig.ctlExe, args)
                model.state = .done
            } catch let processError as ProcessError {
                model.state = .error("(status \(processError.status)) \(processError.stderr)")
            } catch {
                model.state = .error(error.localizedDescription)
            }
        }
        .background(VisualEffectView().ignoresSafeArea())
        .windowHolder(windowHolder)
        .if(!isSheet) { view in
            view.windowRestorability(false)
        }
    }

    private var isSheet: Bool {
        sheetPresented != nil
    }
}
