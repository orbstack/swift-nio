//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct ContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    @State private var actionInProgress = false
    @State private var progressOpacity = 0.0
    @State private var isPresentingConfirm = false

    var body: some View {
        HStack {
            Image("distro_\(record.image.distro)")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
            VStack(alignment: .leading) {
                Text(record.name)
                        .font(.title3)
            }
            Spacer()
            if record.running {
                Button(action: {
                    Task {
                        actionInProgress = true
                        await vmModel.tryStopContainer(record)
                        actionInProgress = false
                    }
                }) {
                    ZStack {
                        Image(systemName: "stop.fill")
                                .opacity(1 - progressOpacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(progressOpacity)
                    }
                }
                        .buttonStyle(BorderlessButtonStyle())
                        .disabled(actionInProgress)
                        .help("Stop \(record.name)")
            } else {
                Button(action: {
                    Task {
                        actionInProgress = true
                        await vmModel.tryStartContainer(record)
                        actionInProgress = false
                    }
                }) {
                    ZStack {
                        Image(systemName: "play.fill")
                                .opacity(1 - progressOpacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(progressOpacity)
                    }
                }
                        .buttonStyle(BorderlessButtonStyle())
                        .disabled(actionInProgress)
                        .help("Start \(record.name)")
            }
        }
        .padding(.vertical, 4)
        .opacity(record.running ? 1 : 0.5)
        .contextMenu {
            Button(role: .destructive, action: {
                self.isPresentingConfirm = true
            }) {
                Label("Delete", systemImage: "trash")
            }
                    .disabled(actionInProgress)
        }
        .confirmationDialog("Delete \(record.name)?",
                isPresented: $isPresentingConfirm) {
            Button("Delete", role: .destructive) {
                Task {
                    actionInProgress = true
                    await vmModel.tryDeleteContainer(record)
                    actionInProgress = false
                }
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .onDoubleClick {
            Task {
                do {
                    try await openTerminal("moon", ["-m", record.name])
                } catch {
                    print(error)
                }
            }
        }
        .onChange(of: actionInProgress) { newValue in
            withAnimation(.spring()) {
                progressOpacity = newValue ? 1 : 0
            }
        }
    }
}