//
// Created by Danny Lin on 6/3/23.
//

import Foundation
import CBridge

class ResultWrapper<T: Any> {
    var result: T?
    private let sem = DispatchSemaphore(value: 0)

    func set(_ result: T) {
        self.result = result
        sem.signal()
    }

    func wait() -> T {
        sem.wait()
        return result!
    }
}

func doGenericErr<T: AnyObject>(_ ptr: UnsafeMutableRawPointer, _ block: @escaping (T) async throws -> Void) -> GResultErr {
    let result = ResultWrapper<GResultErr>()
    Task.detached {
        let obj = Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()
        do {
            try await block(obj)
            result.set(GResultErr(err: nil))
        } catch {
            let prettyError = "\(error)"
            result.set(GResultErr(err: strdup(prettyError)))
        }
    }
    return result.wait()
}

func doGenericErr<T: AnyObject>(_ ptr: UnsafeMutableRawPointer, _ block: @escaping (T) throws -> Void) -> GResultErr {
    let obj = Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()
    do {
        try block(obj)
        return GResultErr(err: nil)
    } catch {
        let prettyError = "\(error)"
        return GResultErr(err: strdup(prettyError))
    }
}

func doGenericErrInt<T: AnyObject>(_ ptr: UnsafeMutableRawPointer, _ block: @escaping (T) async throws -> Int64) -> GResultIntErr {
    let result = ResultWrapper<GResultIntErr>()
    Task.detached {
        let obj = Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()
        do {
            let value = try await block(obj)
            result.set(GResultIntErr(value: value, err: nil))
        } catch {
            let prettyError = "\(error)"
            result.set(GResultIntErr(value: 0, err: strdup(prettyError)))
        }
    }
    return result.wait()
}

func doGenericErrInt<T: AnyObject>(_ ptr: UnsafeMutableRawPointer, _ block: @escaping (T) throws -> Int64) -> GResultIntErr {
    let obj = Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()
    do {
        let value = try block(obj)
        return GResultIntErr(value: value, err: nil)
    } catch {
        let prettyError = "\(error)"
        return GResultIntErr(value: 0, err: strdup(prettyError))
    }
}

func doGeneric<T: AnyObject>(_ ptr: UnsafeMutableRawPointer, _ block: @escaping (T) -> Void) {
    let obj = Unmanaged<T>.fromOpaque(ptr).takeUnretainedValue()
    block(obj)
}

func decodeJson<T: Codable>(_ cStr: UnsafePointer<CChar>) -> T {
    let configJson = String(cString: cStr)
    return try! JSONDecoder().decode(T.self, from: configJson.data(using: .utf8)!)
}