#pragma once

// See "The FOR_EACH macro" in https://www.scs.stanford.edu/~dm/blog/va-opt.html for details on how
// this abomination works. TLDR is that we define `FOR_EACH` to apply the supplied macro to each
// argument in the `__VA_LIST__` alongside a unique name.

#define PARENS ()

#define EXPAND(...) EXPAND4(EXPAND4(EXPAND4(EXPAND4(__VA_ARGS__))))
#define EXPAND4(...) EXPAND3(EXPAND3(EXPAND3(EXPAND3(__VA_ARGS__))))
#define EXPAND3(...) EXPAND2(EXPAND2(EXPAND2(EXPAND2(__VA_ARGS__))))
#define EXPAND2(...) EXPAND1(EXPAND1(EXPAND1(EXPAND1(__VA_ARGS__))))
#define EXPAND1(...) __VA_ARGS__

#define FOR_EACH(macro, ...)                                    \
    __VA_OPT__(EXPAND(FOR_EACH_HELPER(macro, suc, __VA_ARGS__)))

#define FOR_EACH_HELPER(macro, name_gen, a1, ...)                            \
    macro(name_gen, a1)                                                      \
    __VA_OPT__(FOR_EACH_AGAIN PARENS (macro, name_gen ## _suc, __VA_ARGS__))
#define FOR_EACH_AGAIN() FOR_EACH_HELPER
