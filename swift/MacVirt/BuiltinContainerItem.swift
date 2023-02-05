//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct BuiltinContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    @State private var actionInProgress = false

    var body: some View {
        HStack {
            Image("distro_\(record.image.distro)")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)

            VStack(alignment: .leading) {
                Text("Docker")
                        .font(.headline)
                Text("Build and run Docker containers")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
            }
            Spacer()

            let binding = Binding<Bool>(
                get: { record.running },
                set: { newValue in
                    Task {
                        actionInProgress = true
                        do {
                            if newValue {
                                try await self.vmModel.startContainer(record)
                            } else {
                                try await self.vmModel.stopContainer(record)
                            }
                        } catch {
                            print("start err", error)
                        }
                        actionInProgress = false
                    }
                }
            )
            Toggle(isOn: binding) {
            }
            .toggleStyle(SwitchToggleStyle(tint: .blue))
            .disabled(actionInProgress)
        }
        .padding(.vertical, 4)
    }
}