//
// Created by Danny Lin on 2/5/23.
//

import Foundation

func processIsTranslated() -> Bool {
    var ret = Int32(0)
    var size = 4
    let result = sysctlbyname("sysctl.proc_translated", &ret, &size, nil, 0)
    if result == -1 {
        return false
    }
    return ret == 1
}