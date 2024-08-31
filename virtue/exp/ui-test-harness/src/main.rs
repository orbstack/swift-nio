use std::{env, fs, process::Command};

use anyhow::Context;
use formatter::{fmt_indent, log_error, ok_or_exit, ok_or_log};

mod formatter;

fn main() {
    let args = env::args().collect::<Vec<_>>();
    let args = args.iter().map(|v| v.as_str()).collect::<Vec<_>>();

    let test_dir = ok_or_exit(args.get(1).copied().with_context(|| {
        format!(
            "usage: {} <test directory>",
            args.first().copied().unwrap_or("ui-test-harness")
        )
    }));

    for test_entry in ok_or_exit(fs::read_dir(test_dir).context("failed to get directory entries"))
    {
        // Find tests
        let Some(test_entry) = ok_or_log(test_entry.context("failed to read directory entry"))
        else {
            continue;
        };

        let test_entry = test_entry.path();

        if test_entry.extension().and_then(|v| v.to_str()) != Some("test") {
            continue;
        }

        eprintln!("Running {}...", test_entry.to_string_lossy());

        // Parse text segment
        let Some(test_text) =
            ok_or_log(fs::read_to_string(&test_entry).context("failed to read test file"))
        else {
            continue;
        };

        let (Some(commands), Some(expected_output)) = ({
            let mut iter = test_text.split("\n---\n");

            (iter.next(), iter.next())
        }) else {
            log_error(anyhow::anyhow!(
                "file is missing `\\n---\\n` delimiter between test commands and test contents"
            ));
            continue;
        };

        // Run the test
        let Some(actual_output) = ok_or_log(
            Command::new("zsh")
                .arg("-c")
                .arg(commands)
                .output()
                .context("failed to run test script"),
        ) else {
            continue;
        };

        let expected_output = normalize_input(expected_output);
        let actual_output = normalize_input(&String::from_utf8_lossy(&actual_output.stdout));

        if actual_output != expected_output {
            eprintln!(
                "Test failed!\nExpected:\n{}\nActual output:\n{}",
                fmt_indent(expected_output, 4),
                fmt_indent(actual_output, 4),
            );
        } else {
            eprintln!("Test succeeded!");
        }
    }
}

fn normalize_input(v: &str) -> String {
    v.replace("\r\n", "\n").replace("\t", "    ")
}
