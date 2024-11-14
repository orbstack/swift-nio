#include "aprintf.h"

#include <stdio.h>
#include <unistd.h>

#define STB_SPRINTF_IMPLEMENTATION
#define STB_SPRINTF_MIN 64
#define STB_SPRINTF_NOFLOAT
#include "stb_sprintf.h"

struct aprintf_ctx {
    char tmp[STB_SPRINTF_MIN];
};

static char *aprintf_cb(const char *buf, void *user_raw, int len) {
    struct aprintf_ctx *user = (struct aprintf_ctx *) user_raw;
    int ret = write(STDERR_FILENO, buf, len);

    // Okay, so `buf` and `user->tmp` technically alias, which is a bit suspicious, but
    // `stbsp__count_clamp_callback` does this too so I guess it's fine?
    return ret != -1 ? user->tmp : NULL;
}

void _orb_aprintf(const char *fmt, va_list args) {
    struct aprintf_ctx ctx;  // can be uninit, `vsprintfcb` will write to `buf`.
    stbsp_vsprintfcb(aprintf_cb, &ctx, ctx.tmp, fmt, args);
}
