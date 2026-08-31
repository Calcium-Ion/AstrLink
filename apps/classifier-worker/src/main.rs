use std::{
    env,
    io::{self, BufReader, BufWriter},
    path::PathBuf,
    process::ExitCode,
};

use astrlink_classifier_worker::{
    engine::ClassifierEngine,
    protocol::{ClassifyResponse, PROTOCOL_VERSION, read_request, write_ready, write_response},
};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(()) => {
            eprintln!("astrlink-classifier-worker: startup_or_protocol_failure");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), ()> {
    let model_directory = parse_model_directory().ok_or(())?;
    let mut engine = ClassifierEngine::load(&model_directory).map_err(|_| ())?;
    let stdout = io::stdout();
    let mut writer = BufWriter::new(stdout.lock());
    write_ready(&mut writer).map_err(|_| ())?;
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());

    loop {
        let Some(request) = read_request(&mut reader).map_err(|_| ())? else {
            return Ok(());
        };
        let response = if request.version != PROTOCOL_VERSION {
            ClassifyResponse::failure(request.id, "unsupported_protocol")
        } else {
            match engine.classify(&request.text) {
                Ok(result) => ClassifyResponse::success(request.id, result.category, result.logits),
                Err(error) => ClassifyResponse::failure(request.id, error.code()),
            }
        };
        write_response(&mut writer, &response).map_err(|_| ())?;
    }
}

fn parse_model_directory() -> Option<PathBuf> {
    let mut arguments = env::args_os();
    let _executable = arguments.next()?;
    if arguments.next()?.to_str()? != "--model-dir" {
        return None;
    }
    let directory = PathBuf::from(arguments.next()?);
    if arguments.next().is_some() || !directory.is_dir() {
        return None;
    }
    Some(directory)
}
