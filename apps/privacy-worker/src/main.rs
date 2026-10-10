mod decoder;
mod engine;
mod manifest;
mod pplx;
mod priority;
mod protocol;
mod sensitive;
mod span_contract;

use std::{
    env,
    ffi::OsString,
    io::{self, BufReader, BufWriter},
    path::PathBuf,
    process::ExitCode,
};

use engine::PrivacyEngine;
use protocol::{DetectResponse, PROTOCOL_VERSION, read_request, write_ready, write_response};

// Model contract rejections are reported by code so a misconfigured model is
// distinguishable from a generic startup failure. Other error text is never
// echoed.
const CONTRACT_STARTUP_FAILURES: [&str; 4] = [
    "invalid_decoder_contract",
    "invalid_decoder_contract_adapter",
    "invalid_span_contract",
    "invalid_span_contract_adapter",
];
// Core sizes the pool for the device. A core that predates --intra-threads
// gets the old fixed pool, so a mismatched development build still runs.
const DEFAULT_INTRA_THREADS: usize = 2;
const MAX_INTRA_THREADS: usize = 64;

fn main() -> ExitCode {
    priority::lower_process();
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(contract_failure) => {
            eprintln!("astrlink-privacy-worker: startup_or_protocol_failure");
            if let Some(code) = contract_failure {
                eprintln!("astrlink-privacy-worker: {code}");
            }
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), Option<&'static str>> {
    let arguments = parse_arguments(env::args_os()).ok_or(None)?;
    let mut engine = PrivacyEngine::load(&arguments.model_directory, arguments.intra_threads)
        .map_err(|error| {
            let error = error.to_string();
            CONTRACT_STARTUP_FAILURES
                .into_iter()
                .find(|code| *code == error)
        })?;
    let stdout = io::stdout();
    let mut writer = BufWriter::new(stdout.lock());
    write_ready(&mut writer).map_err(|_| None)?;
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());

    loop {
        let Some(request) = read_request(&mut reader).map_err(|_| None)? else {
            return Ok(());
        };
        let response = if request.version != PROTOCOL_VERSION {
            DetectResponse::failure(request.id, "unsupported_protocol")
        } else {
            match engine.detect(&request.texts) {
                Ok(spans) => DetectResponse::success(request.id, spans),
                Err(error) if error.to_string() == "token_limit_exceeded" => {
                    DetectResponse::failure(request.id, "token_limit_exceeded")
                }
                Err(_) => DetectResponse::failure(request.id, "inference_failed"),
            }
        };
        write_response(&mut writer, &response).map_err(|_| None)?;
    }
}

struct Arguments {
    model_directory: PathBuf,
    intra_threads: usize,
}

fn parse_arguments(arguments: impl IntoIterator<Item = OsString>) -> Option<Arguments> {
    let mut arguments = arguments.into_iter();
    let _executable = arguments.next()?;
    if arguments.next()?.to_str()? != "--model-dir" {
        return None;
    }
    let model_directory = PathBuf::from(arguments.next()?);
    let intra_threads = match arguments.next() {
        None => DEFAULT_INTRA_THREADS,
        Some(flag) if flag == "--intra-threads" => arguments
            .next()?
            .to_str()?
            .parse()
            .ok()
            .filter(|threads| (1..=MAX_INTRA_THREADS).contains(threads))?,
        Some(_) => return None,
    };
    if arguments.next().is_some() || !model_directory.is_dir() {
        return None;
    }
    Some(Arguments {
        model_directory,
        intra_threads,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    // Any existing directory stands in for the model directory.
    const DIRECTORY: &str = env!("CARGO_MANIFEST_DIR");

    fn parse(values: &[&str]) -> Option<Arguments> {
        parse_arguments(
            std::iter::once("astrlink-privacy-worker")
                .chain(values.iter().copied())
                .map(OsString::from),
        )
    }

    #[test]
    fn absent_intra_threads_keep_the_old_fixed_pool() {
        let arguments = parse(&["--model-dir", DIRECTORY]).expect("model directory alone");
        assert_eq!(arguments.model_directory, PathBuf::from(DIRECTORY));
        assert_eq!(arguments.intra_threads, DEFAULT_INTRA_THREADS);
    }

    #[test]
    fn accepts_intra_threads_from_one_to_sixty_four() {
        for (value, threads) in [("1", 1), ("4", 4), ("64", 64)] {
            let arguments = parse(&["--model-dir", DIRECTORY, "--intra-threads", value])
                .unwrap_or_else(|| panic!("rejected --intra-threads {value}"));
            assert_eq!(arguments.model_directory, PathBuf::from(DIRECTORY));
            assert_eq!(arguments.intra_threads, threads);
        }
    }

    #[test]
    fn rejects_invalid_intra_threads() {
        for value in ["0", "65", "-1", "four", "4.5", "", " 4"] {
            assert!(
                parse(&["--model-dir", DIRECTORY, "--intra-threads", value]).is_none(),
                "accepted --intra-threads {value:?}"
            );
        }
        let malformed: [&[&str]; 5] = [
            &["--model-dir", DIRECTORY, "--intra-threads"],
            &["--model-dir", DIRECTORY, "--intra-threads", "4", "4"],
            &["--model-dir", DIRECTORY, "--threads", "4"],
            &["--intra-threads", "4", "--model-dir", DIRECTORY],
            &[
                "--model-dir",
                "/nonexistent/astrlink-model",
                "--intra-threads",
                "4",
            ],
        ];
        for arguments in malformed {
            assert!(parse(arguments).is_none(), "accepted {arguments:?}");
        }
    }
}
