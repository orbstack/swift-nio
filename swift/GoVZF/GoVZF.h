//
//  GoVZF.h
//  GoVZF
//
//  Created by Danny Lin on 3/3/23.
//

#import <Foundation/Foundation.h>

//! Project version number for GoVZF.
FOUNDATION_EXPORT double GoVZFVersionNumber;

//! Project version string for GoVZF.
FOUNDATION_EXPORT const unsigned char GoVZFVersionString[];

// In this header, you should import all the public headers of your framework using statements like #import <GoVZF/PublicHeader.h>



#include <stdlib.h>
#include <stdint.h>

void govzf_complete_NewMachine(uintptr_t vmHandle, void* vmWrapperPtr, const char* error, bool rosettaCanceled);
void govzf_complete_Machine_genericErr(uintptr_t vmHandle, const char* error);
void govzf_complete_Machine_genericErrInt(uintptr_t vmHandle, const char* error, int64_t value);

void govzf_event_Machine_onStateChange(uintptr_t vmHandle, int state);