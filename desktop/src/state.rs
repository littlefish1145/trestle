use serde::Deserialize;
use std::collections::{HashMap, VecDeque};

#[derive(Clone, Debug, Deserialize)]
pub struct Workspace {
    pub id: String,
    pub path: String,
    pub name: String,
    pub profile: String,
    pub compiler: String,
    pub projects: Vec<Project>,
    pub targets: Vec<Target>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Project {
    pub name: String,
    pub path: String,
    pub profile: String,
    pub compiler: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Target {
    pub id: String,
    pub member: String,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub sources: Vec<String>,
    #[serde(default)]
    pub dependencies: Vec<String>,
    #[serde(default)]
    pub packages: Vec<String>,
    pub compiler: String,
    #[serde(default)]
    pub compile_flags: Vec<String>,
    #[serde(default)]
    pub link_flags: Vec<String>,
    #[serde(default)]
    pub output_name: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Build {
    pub build_id: String,
    pub workspace_id: String,
    #[serde(default)]
    pub compiler: String,
    #[serde(default)]
    pub duration_ms: u64,
    pub operation: String,
    pub status: String,
    pub options: BuildOptions,
    pub started_at: Option<String>,
    pub ended_at: Option<String>,
    pub progress: Option<Progress>,
    pub error: Option<BuildError>,
    pub log_size: u64,
    pub log_capped: bool,
    #[serde(default)]
    pub log_paths: Vec<String>,
    pub timing_count: usize,
    #[serde(default)]
    pub artifacts: HashMap<String, String>,
}

impl Build {
    pub fn active(&self) -> bool {
        matches!(self.status.as_str(), "queued" | "preparing" | "running")
    }
}

#[derive(Clone, Debug, Deserialize)]
pub struct BuildOptions {
    #[serde(default)]
    pub member: String,
    #[serde(default)]
    pub targets: Vec<String>,
    #[serde(default)]
    pub profile: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Progress {
    pub member: String,
    pub completed: usize,
    pub total: usize,
    pub running: usize,
}

#[derive(Clone, Debug, Deserialize)]
pub struct BuildError {
    pub message: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct LogPage {
    pub offset: u64,
    pub next_offset: u64,
    pub text: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Analysis {
    pub build: Build,
    pub timings: Vec<Timing>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Timing {
    pub member: String,
    pub output: String,
    #[serde(default)]
    pub target: String,
    pub start_ms: u64,
    pub end_ms: u64,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct Component {
    pub name: String,
    pub family: String,
    pub path: String,
    pub version: String,
    pub ready: bool,
    pub detail: String,
    pub execution: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Toolchain {
    pub kind: String,
    pub cc: String,
    pub cxx: String,
    pub version: String,
    pub target: String,
}

const MAX_LINES: usize = 4000;
const MAX_LINE_BYTES: usize = 8192;

#[derive(Default)]
pub struct BuildOutput {
    pub offset: u64,
    pub lines: VecDeque<String>,
    partial: String,
    discarding_line: bool,
}

impl BuildOutput {
    pub fn append(&mut self, page: LogPage, ended: bool) {
        if page.offset != self.offset || page.next_offset < page.offset {
            return;
        }
        self.offset = page.next_offset;
        for piece in page.text.split_inclusive('\n') {
            let remaining = MAX_LINE_BYTES.saturating_sub(self.partial.len());
            if !self.discarding_line {
                if piece.len() > remaining {
                    let end = piece.floor_char_boundary(remaining);
                    self.partial.push_str(&piece[..end]);
                    self.partial.push_str(" …");
                    self.discarding_line = true;
                } else {
                    self.partial.push_str(piece);
                }
            }
            if piece.ends_with('\n') {
                self.push_line();
            }
        }
        if ended && !self.partial.is_empty() {
            self.push_line();
        }
    }

    fn push_line(&mut self) {
        let value = std::mem::take(&mut self.partial);
        self.lines
            .push_back(value.trim_end_matches(['\r', '\n']).into());
        if self.lines.len() > MAX_LINES {
            self.lines.pop_front();
        }
        self.discarding_line = false;
    }
}

// The parser accepts only a file/line/column and explicit compiler severity.
// Unrecognized tool diagnostics remain available in the raw output.
pub fn problem(line: &str) -> Option<(&str, u32, u32, &str, &str)> {
    for (marker, level) in [
        (": fatal error: ", "Error"),
        (": error: ", "Error"),
        (": warning: ", "Warning"),
        ("): error ", "Error"),
        ("): warning ", "Warning"),
        ("): fatal error ", "Error"),
    ] {
        let Some((loc, message)) = line.split_once(marker) else {
            continue;
        };
        let (file, row, column) = if marker.starts_with(')') {
            let (file, coordinate) = loc.rsplit_once('(')?;
            let (row, column) = coordinate.split_once(',').unwrap_or((coordinate, "0"));
            (file, row.trim().parse().ok()?, column.trim().parse().ok()?)
        } else {
            let mut parts = loc.rsplitn(3, ':');
            let column = parts.next()?.parse().ok()?;
            let row = parts.next()?.parse().ok()?;
            (parts.next()?, row, column)
        };
        if !file.is_empty() && row > 0 {
            return Some((file, row, column, level, message));
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn log_replay_preserves_partial_lines_and_ignores_repeats() {
        let mut log = BuildOutput::default();
        log.append(
            LogPage {
                offset: 0,
                next_offset: 3,
                text: "abc".into(),
            },
            false,
        );
        assert!(log.lines.is_empty());
        log.append(
            LogPage {
                offset: 0,
                next_offset: 3,
                text: "bad".into(),
            },
            false,
        );
        log.append(
            LogPage {
                offset: 3,
                next_offset: 6,
                text: "de\nf".into(),
            },
            true,
        );
        assert_eq!(log.lines, ["abcde", "f"]);
    }

    #[test]
    fn supports_gcc_and_msvc_locations_without_guessing() {
        assert_eq!(
            problem("C:\\tmp\\a.cpp:3:5: error: bad"),
            Some(("C:\\tmp\\a.cpp", 3, 5, "Error", "bad"))
        );
        assert_eq!(
            problem("a.cpp(2,7): warning C0001: bad"),
            Some(("a.cpp", 2, 7, "Warning", "C0001: bad"))
        );
        assert!(problem("ninja: build stopped: subcommand failed").is_none());
    }
}

#[cfg(test)]
mod measurements {
    use super::*;

    #[test]
    fn replay_10000_lines() {
        let lines = (0..10000)
            .map(|i| format!("[{i}/10000] compiling src/target_{i}.cpp\n"))
            .collect::<String>();
        let mut output = BuildOutput::default();
        let started = std::time::Instant::now();
        for page in lines.as_bytes().chunks(65536) {
            let offset = output.offset;
            output.append(
                LogPage {
                    offset,
                    next_offset: offset + page.len() as u64,
                    text: std::str::from_utf8(page).unwrap().to_owned(),
                },
                false,
            );
        }
        assert_eq!(output.lines.len(), MAX_LINES);
        assert!(output.lines.back().unwrap().contains("target_9999.cpp"));
        eprintln!(
            "10000 log lines / {} bytes: {:?} replay into {} retained rows",
            lines.len(),
            started.elapsed(),
            output.lines.len()
        );
    }
}
