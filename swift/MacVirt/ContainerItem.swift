//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct ContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    @State private var actionInProgress = false
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
                        .font(.headline)
            }
            Spacer()
            if record.running {
                Button(action: {
                    Task {
                        actionInProgress = true
                        do {
                            try await self.vmModel.stopContainer(record)
                        } catch {
                            print("stop err", error)
                        }
                        actionInProgress = false
                    }
                }) {
                    if actionInProgress {
                        ProgressView()
                    } else {
                        Label("Stop", systemImage: "stop.fill")
                    }
                }
                        .buttonStyle(BorderlessButtonStyle())
                        .disabled(actionInProgress)
            } else {
                Button(action: {
                    Task {
                        actionInProgress = true
                        do {
                            try await self.vmModel.startContainer(record)
                        } catch {
                            print("start err", error)
                        }
                        actionInProgress = false
                    }
                }) {
                    if actionInProgress {
                        ProgressView()
                    } else {
                        Label("Start", systemImage: "play.fill")
                    }
                }
                        .buttonStyle(BorderlessButtonStyle())
                        .disabled(actionInProgress)
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
                    do {
                        try await self.vmModel.deleteContainer(record)
                    } catch {
                        print("delete err", error)
                    }
                    actionInProgress = false
                }
            }
        } message: {
            Text("Data will be permanently lost.")
        }
    }
}