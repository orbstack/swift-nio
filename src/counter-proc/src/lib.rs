use std::str::FromStr;

use aho_corasick::AhoCorasick;
use litrs::{BoolLit, StringLit};
use proc_macro::{Delimiter, Group, Literal, Punct, Spacing, TokenStream, TokenTree};

const ENV_COMPILED_COUNTERS: &str =
    include_str!(concat!(env!("OUT_DIR"), "/env_compiled_counters.txt"));

#[proc_macro]
pub fn cfg_aho(input: TokenStream) -> TokenStream {
    // Parse input
    let mut input = input.into_iter();

    let Some(TokenTree::Group(if_okay)) = input.next() else {
        return pm_error("expected group as first argument");
    };

    let Some(TokenTree::Group(if_not_okay)) = input.next() else {
        return pm_error("expected group as second argument");
    };

    let are_debug_assertions_enabled = match input.next() {
        Some(tt) => strip_parent_macro_groups(tt),
        None => return pm_error("expected debug assertion status as third argument"),
    };

    let are_debug_assertions_enabled = match BoolLit::try_from(are_debug_assertions_enabled) {
        Ok(name) => name,
        Err(err) => {
            return pm_error(&format!(
                "expected debug assertion status as third argument: {err}"
            ))
        }
    };

    let name = match input.next() {
        Some(tt) => strip_parent_macro_groups(tt),
        None => return if_okay.stream(),
    };

    let name = match StringLit::try_from(name) {
        Ok(name) => name,
        Err(err) => {
            return pm_error(&format!(
                "expected filter string to be a string literal: {err}"
            ))
        }
    };

    if input.next().is_some() {
        return pm_error("expected at most four arguments");
    }

    // Build a filter
    if ENV_COMPILED_COUNTERS == "COUNTER_SENTINEL_NOT_SPECIFIED" {
        if are_debug_assertions_enabled.value() {
            if_okay.stream()
        } else {
            if_not_okay.stream()
        }
    } else {
        let filter = AhoCorasick::builder()
            .ascii_case_insensitive(true)
            .build(ENV_COMPILED_COUNTERS.split(','))
            .unwrap();

        if filter.is_match(name.value()) {
            if_okay.stream()
        } else {
            if_not_okay.stream()
        }
    }
}

fn pm_error(text: &str) -> TokenStream {
    TokenStream::from_iter(
        TokenStream::from_str("::core::compile_error!")
            .unwrap()
            .into_iter()
            .chain([
                TokenTree::Group(Group::new(
                    Delimiter::Parenthesis,
                    TokenStream::from_iter([TokenTree::Literal(Literal::string(text))]),
                )),
                TokenTree::Punct(Punct::new(';', Spacing::Joint)),
            ]),
    )
}

fn strip_parent_macro_groups(mut token: TokenTree) -> TokenTree {
    loop {
        let TokenTree::Group(group) = token.clone() else {
            break;
        };

        if group.delimiter() != Delimiter::None {
            break;
        }

        let mut group = group.stream().into_iter();
        let Some(first) = group.next() else {
            break;
        };
        if group.next().is_some() {
            break;
        }

        token = first;
    }

    token
}
