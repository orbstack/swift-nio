#include "aprintf.h"

#include <stdio.h>
#include <unistd.h>

#define STB_SPRINTF_IMPLEMENTATION
#define STB_SPRINTF_MIN 64
#define STB_SPRINTF_NOFLOAT
#include "stb_sprintf.h"

typedef struct aprintf_ctx {
    char tmp[STB_SPRINTF_MIN];
} aprintf_ctx_t;

static char *aprintf_cb(char const *buf, void *user_raw, int len) {
    aprintf_ctx_t *user = (aprintf_ctx_t *) user_raw;
    int ret = write(STDERR_FILENO, buf, len);

    // Okay, so `buf` and `user->tmp` technically alias, which is a bit suspicious, but
    // `stbsp__count_clamp_callback` does this too so I guess it's fine?
    return ret != -1 ? user->tmp : NULL;
}

void _orb_aprintf(char const *fmt, va_list args) {
    aprintf_ctx_t ctx;  // can be uninit, `vsprintfcb` will write to `buf`.
    stbsp_vsprintfcb(aprintf_cb, &ctx, ctx.tmp, fmt, args);
}
