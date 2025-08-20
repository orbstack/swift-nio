import SwiftUI
import Combine

struct CreateForm<Content: View>: View {
    let submitCommand: PassthroughSubject<Void, Never>?
    let content: Content
    let onSubmit: () -> Void

    @State private var preferences = [ValidateFunc]()

    init(
        submitCommand: PassthroughSubject<Void, Never>? = nil,
        @ViewBuilder content: () -> Content,
        onSubmit: @escaping () -> Void
    ) {
        self.content = content()
        self.onSubmit = onSubmit
        self.submitCommand = submitCommand
    }

    var body: some View {
        Form {
            content
        }
        .formStyle(.grouped)
        .onSubmit(doSubmit)
        .ifLet(submitCommand) { view, stream in
            view.onReceive(stream) { _ in
                doSubmit()
            }
        }
        .environment(\.createSubmitFunc, doSubmit)
        .onPreferenceChange(ValidatedFieldKey.self) { preferences in
            self.preferences = preferences
        }
    }

    private func doSubmit() {
        var valid = true
        // validate all to show all errors
        for preference in preferences {
            valid = valid && preference.value()
        }
        if valid {
            onSubmit()
        }
    }
}

typealias CreateButtonRow = SettingsFooter

struct ValidatedTextField<Label: View>: View {
    let text: Binding<String>
    @ViewBuilder let label: () -> Label
    let prompt: Text?
    let validate: (String) -> String?

    @State private var errorText: String?
    @State private var errorHeight = 0.0

    init(
        text: Binding<String>, prompt: Text? = nil, label: @escaping () -> Label,
        validate: @escaping (String) -> String?
    ) {
        self.label = label
        self.text = text
        self.prompt = prompt
        self.validate = validate
    }

    var body: some View {
        TextField(text: text, prompt: prompt) {
            label()

            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundColor(.red)
                    .frame(maxHeight: errorHeight)
                    .clipped()
            }
        }
        .preference(key: ValidatedFieldKey.self, value: [UniqueEquatable(value: doValidate)])
    }

    func doValidate() -> Bool {
        errorText = validate(text.wrappedValue)

        withAnimation(Animation.spring()) {
            if errorText != nil {
                errorHeight = NSFont.preferredFont(forTextStyle: .caption1).pointSize
            } else {
                errorHeight = 0
            }
        }

        return errorText == nil
    }
}

extension ValidatedTextField where Label == Text {
    init(
        _ title: String, text: Binding<String>, prompt: Text? = nil,
        validate: @escaping (String) -> String?
    ) {
        self.init(text: text, prompt: prompt, label: { Text(title) }, validate: validate)
    }
}

private typealias ValidateFunc = UniqueEquatable<() -> Bool>

private struct ValidatedFieldKey: PreferenceKey {
    static var defaultValue = [ValidateFunc]()

    static func reduce(value: inout [ValidateFunc], nextValue: () -> [ValidateFunc]) {
        let nextVal = nextValue()
        value.append(contentsOf: nextVal)
    }
}

extension EnvironmentValues {
    var createSubmitFunc: () -> Void {
        get { self[CreateSubmitFuncKey.self] }
        set { self[CreateSubmitFuncKey.self] = newValue }
    }
}

private struct CreateSubmitFuncKey: EnvironmentKey {
    static var defaultValue: () -> Void = {}
}

struct CreateSubmitButton: View {
    let title: String
    @Environment(\.createSubmitFunc) private var submitFunc

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Button(title) {
            submitFunc()
        }
    }
}
