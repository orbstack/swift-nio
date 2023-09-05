//
// Created by Danny Lin on 9/5/23.
//

import Foundation
import SwiftUI
import Combine

extension ObservableObject {
    func freeze() -> FrozenObservableObject<Self> {
        .init(obj: self)
    }
}

@dynamicMemberLookup
class FrozenObservableObject<Object: ObservableObject>: ObservableObject {
    fileprivate var obj: Object

    init(obj: Object) {
        self.obj = obj
    }

    subscript<Key>(dynamicMember keyPath: WritableKeyPath<Object, Key>) -> Key {
        get { obj[keyPath: keyPath] }
        set { obj[keyPath: keyPath] = newValue }
    }
}

extension View {
    func environmentObjectWithFreeze<Object: ObservableObject>(_ obj: Object) -> some View {
        self
            .environmentObject(obj)
            .environmentObject(obj.freeze())
    }
}

@propertyWrapper
struct FrozenEnvironmentObject<Object: ObservableObject>: DynamicProperty {
    @EnvironmentObject private var frozenObj: FrozenObservableObject<Object>

    var wrappedValue: Object {
        frozenObj.obj
    }

    var projectedValue: Binding<Object> {
        $frozenObj.obj
    }
}

@propertyWrapper
struct FrozenEnvKey<Object: ObservableObject, Value>: DynamicProperty {
    @EnvironmentObject private var frozenObj: FrozenObservableObject<Object>
    @StateObject private var keyObserver = KeyMirror()

    var wrappedValue: Value {
        get { keyObserver.value }
        nonmutating set { keyObserver.value = newValue }
    }

    var projectedValue: Binding<Value> {
        $keyObserver.value
    }

    init(_ valueKeyPath: KeyPath<Object, Value>,
         _ publisherKeyPath: KeyPath<Object, Published<Value>.Publisher>) {
        keyObserver.setup(initialValue: frozenObj.obj[keyPath: valueKeyPath],
                          publisher: frozenObj.obj[keyPath: publisherKeyPath])
    }

    private class KeyMirror: ObservableObject {
        var value: Value!
        private var cancellable: AnyCancellable?

        func setup(initialValue: Value, publisher: Published<Value>.Publisher) {
            value = initialValue
            cancellable = publisher.sink { [weak self] in
                guard let self else { return }
                self.objectWillChange.send()
                self.value = $0
            }
        }
    }
}