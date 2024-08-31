use std::{
    fmt::{self, Write},
    panic::Location,
    process::exit,
};

// === Write Adapters === //

pub trait Preprocessor {
    type State;

    fn init(&self) -> Self::State;

    fn write(&self, f: &mut fmt::Formatter<'_>, state: &mut Self::State, s: &str) -> fmt::Result;
}

pub struct PreprocessWriter<'a, 'b, P: Preprocessor> {
    pub target: &'a mut fmt::Formatter<'b>,
    pub config: &'a P,
    pub state: P::State,
}

impl<'a, 'b, P: Preprocessor> PreprocessWriter<'a, 'b, P> {
    pub fn new(target: &'a mut fmt::Formatter<'b>, config: &'a P) -> Self {
        Self {
            target,
            config,
            state: config.init(),
        }
    }
}

impl<P: Preprocessor> fmt::Write for PreprocessWriter<'_, '_, P> {
    fn write_str(&mut self, s: &str) -> fmt::Result {
        self.config.write(self.target, &mut self.state, s)
    }
}

pub struct Preprocess<T, P>(pub T, pub P)
where
    T: fmt::Display,
    P: Preprocessor;

impl<T, P> fmt::Display for Preprocess<T, P>
where
    T: fmt::Display,
    P: Preprocessor,
{
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(PreprocessWriter::new(f, &self.1), "{}", self.0)
    }
}

pub struct IdentPreprocessor(pub usize);

pub struct IdentPreprocessorState {
    pushed_first_line: bool,
}

impl Preprocessor for IdentPreprocessor {
    type State = IdentPreprocessorState;

    fn init(&self) -> Self::State {
        IdentPreprocessorState {
            pushed_first_line: false,
        }
    }

    fn write(&self, f: &mut fmt::Formatter<'_>, state: &mut Self::State, s: &str) -> fmt::Result {
        if !state.pushed_first_line {
            state.pushed_first_line = true;

            for _ in 0..self.0 {
                f.write_char(' ')?;
            }
        }

        for part in s.split_inclusive('\n') {
            f.write_str(part)?;

            if part.ends_with('\n') {
                for _ in 0..self.0 {
                    f.write_char(' ')?;
                }
            }
        }

        Ok(())
    }
}

pub fn fmt_indent(target: impl fmt::Display, level: usize) -> impl fmt::Display {
    Preprocess(target, IdentPreprocessor(level))
}

// === Error Printing === //

#[track_caller]
pub fn log_error(e: anyhow::Error) {
    eprintln!(
        "error at {}:\n{}",
        Location::caller(),
        fmt_indent(format_args!("{e:?}"), 4)
    );
}

#[track_caller]
pub fn ok_or_log<T>(v: anyhow::Result<T>) -> Option<T> {
    match v {
        Ok(v) => Some(v),
        Err(e) => {
            log_error(e);
            None
        }
    }
}

#[track_caller]
pub fn ok_or_exit<T>(v: anyhow::Result<T>) -> T {
    match v {
        Ok(v) => v,
        Err(e) => {
            log_error(e);
            exit(1);
        }
    }
}
