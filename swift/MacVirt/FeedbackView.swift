//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

private let charLimit = 30000
private let emailCharLimit = 256

private class FeedbackViewModel: ObservableObject {
    let apiClient = ApiClient()
}

private struct FeedbackRequest: Codable {
    let text: String
    let email: String
}

private struct HttpError: LocalizedError {
    let statusCode: Int

    var errorDescription: String {
        "HTTP Error \(statusCode)"
    }
}

class ApiClient {
    func sendFeedback(text: String, email: String) async throws {
        let url = URL(string: "\(AppConfig.apiBaseUrl)/api/v1/app/feedbacks")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.httpBody = try JSONEncoder().encode(FeedbackRequest(text: text, email: email))
        let (_, resp) = try await URLSession.shared.data(for: request)
        NSLog("API call => \(resp)")
        if let resp = resp as? HTTPURLResponse {
            if resp.statusCode != 200 {
                throw HttpError(statusCode: resp.statusCode)
            }
        } else {
            throw HttpError(statusCode: -1)
        }
    }
}

struct FeedbackView: View {
    @StateObject private var feedbackModel = FeedbackViewModel()
    @StateObject private var windowHolder = WindowHolder()

    // TextEditor doesn't like Published. perf issues
    @State private var feedbackText = ""
    @State private var email = ""
    @State private var sendInProgress = false
    @State private var sendError: Error?

    var body: some View {
        VStack {
            Text("Send Feedback")
            .font(.title)
            .fontWeight(.bold)
            .padding(.bottom, 8)
            Text("What's on your mind? An issue, suggestion, or a nice note?")
            .foregroundColor(.secondary)
            .padding(.bottom, 16)

            ZStack(alignment: .center) {
                // fix layout shift: must always be here...
                VStack(spacing: 16) {
                    ProgressView()
                }
                .opacity(sendInProgress ? 1 : 0)

                VStack {
                    let textBinding = Binding<String>(
                            get: { feedbackText },
                            set: {
                                feedbackText = String($0.prefix(charLimit))
                            }
                    )
                    TextEditor(text: textBinding)
                    .frame(height: 200)
                    // rounded
                    .clipShape(RoundedRectangle(cornerRadius: 4, style: .continuous))
                    .padding(.bottom, 16)
                    .font(.body)

                    Text("Should we follow up via email?")
                    .padding(.bottom, 8)
                    TextField("Email (optional)", text: $email)
                    .textFieldStyle(RoundedBorderTextFieldStyle())
                    .padding(.bottom, 16)

                    if let sendError {
                        Text("Failed to send: \(sendError.localizedDescription)")
                        .lineLimit(2)
                        .foregroundColor(.red)
                        .padding(.bottom, 16)
                    }
                }
                .opacity(sendInProgress ? 0 : 1)
                .disabled(sendInProgress)
            }

            HStack {
                Spacer()

                Button("Send") {
                    Task {
                        sendInProgress = true
                        do {
                            try await feedbackModel.apiClient.sendFeedback(text: feedbackText, email: email)
                            windowHolder.window?.close()
                        } catch {
                            sendError = error
                        }
                        sendInProgress = false
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(feedbackText.count < 5 || feedbackText.count > charLimit || sendInProgress)
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .frame(width: 450)
        .padding(24)
    }
}
