use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc::{self, Receiver, SyncSender, TrySendError},
    },
    thread,
    time::{Duration, Instant},
};

const MAX_FRAME: u64 = 32 * 1024 * 1024;

#[derive(Debug, Deserialize)]
pub struct Failure {
    pub code: i32,
    pub message: String,
}

#[derive(Debug, Deserialize)]
pub struct Event {
    pub version: u32,
    pub build_id: String,
    pub sequence: u64,
    pub timestamp: String,
    pub data: Value,
}

#[derive(Debug)]
pub enum Message {
    Response {
        id: u64,
        result: Result<Value, Failure>,
    },
    Event {
        method: String,
        event: Event,
    },
    Disconnected(String),
}

pub fn decode(line: &[u8]) -> Result<Message, String> {
    let value: Value = serde_json::from_slice(line).map_err(|error| error.to_string())?;
    if value["jsonrpc"] != "2.0" {
        return Err("Core returned an invalid JSON-RPC version".into());
    }
    if let Some(method) = value["method"].as_str() {
        let event: Event =
            serde_json::from_value(value["params"].clone()).map_err(|error| error.to_string())?;
        if event.version != 1 {
            return Err(format!("Unsupported event version {}", event.version));
        }
        return Ok(Message::Event {
            method: method.into(),
            event,
        });
    }
    let id = value["id"].as_u64().ok_or("Missing numeric response ID")?;
    let result = match (value.get("result"), value.get("error")) {
        (Some(result), None) => Ok(result.clone()),
        (None, Some(error)) => {
            Err(serde_json::from_value(error.clone()).map_err(|error| error.to_string())?)
        }
        _ => return Err("Core response must contain exactly one of result and error".into()),
    };
    Ok(Message::Response { id, result })
}

pub struct Client {
    outgoing: Option<SyncSender<Value>>,
    replies: Receiver<Message>,
    events: Receiver<Message>,
    closed: Arc<AtomicBool>,
    supervisor: Option<thread::JoinHandle<()>>,
    next: u64,
}

impl Client {
    pub fn start(executable: &Path) -> Result<Self, String> {
        let mut command = Command::new(executable);
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x08000000;
            command.creation_flags(CREATE_NO_WINDOW);
        }
        if let Some(directory) = desktop_state_dir() {
            command.env("TRESTLE_DESKTOP_STATE_DIR", directory);
        }
        let mut child = command
            .arg("desktop-rpc")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .map_err(|error| format!("Cannot start Core ({}): {error}", executable.display()))?;

        let mut stdin = child.stdin.take().expect("piped stdin");
        let stdout = child.stdout.take().expect("piped stdout");
        let (outgoing, requests) = mpsc::sync_channel::<Value>(32);
        let (reply_tx, replies) = mpsc::sync_channel(32);
        let (event_tx, events) = mpsc::sync_channel(256);
        let closed = Arc::new(AtomicBool::new(false));

        thread::spawn(move || {
            while let Ok(request) = requests.recv() {
                if serde_json::to_writer(&mut stdin, &request).is_err()
                    || stdin.write_all(b"\n").is_err()
                    || stdin.flush().is_err()
                {
                    break;
                }
            }
        });
        let reader_tx = reply_tx.clone();
        let reader_closed = closed.clone();
        thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            let reason = loop {
                let mut frame = Vec::new();
                match reader
                    .by_ref()
                    .take(MAX_FRAME)
                    .read_until(b'\n', &mut frame)
                {
                    Ok(0) => break "The Core closed its output stream".to_string(),
                    Ok(_) if frame.last() != Some(&b'\n') => {
                        break "The Core returned an oversized or incomplete frame".into();
                    }
                    Err(error) => break format!("Cannot read Core output: {error}"),
                    Ok(_) => match decode(&frame) {
                        Ok(message @ Message::Event { .. }) => {
                            // Status polls and offset-based replay recover queue gaps.
                            let _ = event_tx.try_send(message);
                        }
                        Ok(message) => {
                            if reader_tx.send(message).is_err() {
                                return;
                            }
                        }
                        Err(error) => break format!("Invalid Core message: {error}"),
                    },
                }
            };
            reader_closed.store(true, Ordering::Release);
            let _ = reader_tx.send(Message::Disconnected(reason));
        });
        let shutdown = closed.clone();
        let supervisor = thread::spawn(move || supervise(&mut child, &shutdown, reply_tx));
        Ok(Self {
            outgoing: Some(outgoing),
            replies,
            events,
            closed,
            supervisor: Some(supervisor),
            next: 0,
        })
    }

    pub fn send(&mut self, method: &str, params: Value) -> Result<u64, String> {
        if self.closed.load(Ordering::Acquire) {
            return Err("Core is disconnected".into());
        }
        self.next = self.next.checked_add(1).ok_or("Request ID limit reached")?;
        let id = self.next;
        let request = json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params});
        self.outgoing
            .as_ref()
            .ok_or("Core is disconnected")?
            .try_send(request)
            .map_err(|error| match error {
                TrySendError::Full(_) => "Core request queue is full".to_string(),
                TrySendError::Disconnected(_) => "Core is disconnected".to_string(),
            })?;
        Ok(id)
    }

    pub fn drain(&self) -> Vec<Message> {
        self.replies
            .try_iter()
            .take(32)
            .chain(self.events.try_iter().take(128))
            .collect()
    }

    pub fn shutdown(mut self) -> Result<(), String> {
        self.disconnect();
        match self.supervisor.take() {
            Some(handle) => handle.join().map_err(|_| "Core supervisor panicked".into()),
            None => Ok(()),
        }
    }

    fn disconnect(&mut self) {
        self.outgoing.take();
        self.closed.store(true, Ordering::Release);
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        self.disconnect();
    }
}

fn supervise(child: &mut Child, shutdown: &AtomicBool, replies: SyncSender<Message>) {
    let mut deadline = None;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                let _ = replies.try_send(Message::Disconnected(format!("Core exited ({status})")));
                return;
            }
            Err(error) => {
                let _ = replies.try_send(Message::Disconnected(format!(
                    "Cannot wait for Core: {error}"
                )));
                break;
            }
            Ok(None) => {}
        }
        if shutdown.load(Ordering::Acquire)
            && Instant::now()
                >= *deadline.get_or_insert_with(|| {
                    Instant::now() + Duration::from_secs(if cfg!(windows) { 8 } else { 4 })
                })
        {
            break;
        }
        thread::sleep(Duration::from_millis(30));
    }
    let _ = child.kill();
    let _ = child.wait();
}

pub fn core_path() -> PathBuf {
    if let Some(path) = std::env::var_os("TRESTLE_CORE") {
        return path.into();
    }
    let executable = if cfg!(windows) {
        "trestle.exe"
    } else {
        "trestle"
    };
    if let Ok(path) = std::env::current_exe() {
        let sibling = path.with_file_name(executable);
        if sibling.is_file() {
            return sibling;
        }
    }
    PathBuf::from(executable)
}

pub fn desktop_state_dir() -> Option<PathBuf> {
    let root = if cfg!(windows) {
        std::env::var_os("LOCALAPPDATA").map(PathBuf::from)
    } else if cfg!(target_os = "macos") {
        std::env::var_os("HOME").map(|home| PathBuf::from(home).join("Library/Application Support"))
    } else {
        std::env::var_os("XDG_STATE_HOME")
            .map(PathBuf::from)
            .or_else(|| {
                std::env::var_os("HOME").map(|home| PathBuf::from(home).join(".local/state"))
            })
    };
    root.filter(|path| path.is_absolute())
        .map(|root| root.join("trestle-desktop"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decodes_results_and_errors_and_rejects_mixed_envelopes() {
        assert!(matches!(
            decode(br#"{"jsonrpc":"2.0","id":7,"result":{"version":1}}"#).unwrap(),
            Message::Response {
                id: 7,
                result: Ok(_)
            }
        ));
        assert!(matches!(
            decode(br#"{"jsonrpc":"2.0","id":7,"error":{"code":-32001,"message":"incompatible"}}"#)
                .unwrap(),
            Message::Response {
                id: 7,
                result: Err(Failure { code: -32001, .. })
            }
        ));
        assert!(decode(br#"{"jsonrpc":"2.0","id":7,"result":null,"error":null}"#).is_err());
        assert!(decode(b"compiler output").is_err());
    }
}
