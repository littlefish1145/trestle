use gpui::*;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    path::PathBuf,
    rc::Rc,
    time::{Duration, Instant},
};
use trestle_desktop::{
    graph::Graph,
    protocol::{Client, Message, core_path, desktop_state_dir},
    state::{
        Analysis, Build, BuildOutput, Component, GeneratedPage, LogPage, Target, Toolchain,
        Workspace, problem,
    },
};

pub(crate) mod view;

actions!(
    trestle,
    [OpenWorkspace, BuildSelected, Commands, Search, CloseWindow]
);

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Page {
    Targets,
    Graph,
    Toolchains,
    Settings,
}
#[derive(Clone, Copy, PartialEq, Eq)]
enum Drawer {
    Output,
    Problems,
    History,
    Analysis,
}
#[derive(Clone, Copy, PartialEq, Eq)]
enum Input {
    None,
    Find,
    Path,
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Direction {
    Direct,
    Transitive,
    Reverse,
}

enum Pending {
    Handshake,
    Open { activate: bool, token: u64 },
    Update,
    Start(String),
    Status(String),
    Cancel,
    Log(String),
    History(String),
    Analysis(String),
    Inventory,
    Detect(String, String),
    TargetCommands(String, String),
}

#[derive(Default)]
struct Job {
    state: Option<Build>,
    output: BuildOutput,
    sequence: u64,
    needs_poll: bool,
}

pub struct Desktop {
    client: Option<Client>,
    requests: HashMap<u64, (Instant, Pending)>,
    focus: FocusHandle,
    ready: bool,
    connecting: bool,
    closing: bool,
    error: Option<String>,
    workspaces: Vec<Workspace>,
    active: Option<String>,
    target: Option<String>,
    project_filter: Option<String>,
    generated: Option<(String, String, GeneratedPage)>,
    profile: String,
    recent: Vec<String>,
    open_token: u64,
    initial: Option<PathBuf>,
    selected_builds: HashMap<String, String>,
    jobs: HashMap<String, Job>,
    inventory: Vec<Component>,
    detected: Option<Toolchain>,
    history: Vec<Build>,
    analysis: Option<Analysis>,
    comparison: Option<Analysis>,
    show_ninja_details: bool,
    last_poll: Instant,
    build_at: Option<Instant>,
    light: bool,
    page: Page,
    drawer: Drawer,
    drawer_height: f32,
    resize_at: Option<(Pixels, f32)>,
    follow: bool,
    log_scroll: UniformListScrollHandle,
    input: Input,
    pub(crate) query: String,
    path_input: String,
    commands: bool,
    pub graph: Option<Rc<Graph>>,
    pub(crate) graph_error: Option<String>,
    pub zoom: f32,
    pub graph_x: f32,
    pub graph_y: f32,
    pub direction: Direction,
    pub graph_focus_only: bool,
    pub graph_measured_only: bool,
    pub artifact_times: HashMap<String, u64>,
    pub graph_drag: Option<Point<Pixels>>,
    pub graph_related: Rc<std::collections::HashSet<usize>>,
    pub graph_collapsed: std::collections::HashSet<usize>,
    pub graph_expanded: Option<Rc<Vec<bool>>>,
}

impl Desktop {
    pub fn new(initial: Option<PathBuf>, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window);
        let weak = cx.weak_entity();
        window.on_window_should_close(cx, move |window, app| {
            let _ = weak.update(app, |this, cx| this.close(window, cx));
            false
        });
        let timer = cx.background_executor().clone();
        cx.spawn(async move |this, cx| {
            loop {
                timer.timer(Duration::from_millis(45)).await;
                if this.update(cx, |this, cx| this.tick(cx)).is_err() {
                    break;
                }
            }
        })
        .detach();
        let mut this = Self {
            client: None,
            requests: HashMap::new(),
            focus,
            ready: false,
            connecting: false,
            closing: false,
            error: None,
            workspaces: Vec::new(),
            active: None,
            target: None,
            project_filter: None,
            generated: None,
            profile: String::new(),
            recent: Vec::new(),
            open_token: 0,
            initial,
            selected_builds: HashMap::new(),
            jobs: HashMap::new(),
            inventory: Vec::new(),
            detected: None,
            history: Vec::new(),
            analysis: None,
            comparison: None,
            show_ninja_details: false,
            last_poll: Instant::now(),
            build_at: None,
            light: false,
            page: Page::Targets,
            drawer: Drawer::Output,
            drawer_height: 250.,
            resize_at: None,
            follow: true,
            log_scroll: UniformListScrollHandle::new(),
            input: Input::None,
            query: String::new(),
            path_input: String::new(),
            commands: false,
            graph: None,
            graph_error: None,
            zoom: 1.,
            graph_x: 0.,
            graph_y: 0.,
            direction: Direction::Direct,
            graph_focus_only: false,
            graph_measured_only: false,
            artifact_times: HashMap::new(),
            graph_drag: None,
            graph_related: Rc::default(),
            graph_collapsed: Default::default(),
            graph_expanded: None,
        };
        let load = cx.background_executor().spawn(async {
            recent_path()
                .and_then(|path| std::fs::read(path).ok())
                .and_then(|data| serde_json::from_slice::<Vec<String>>(&data).ok())
                .unwrap_or_default()
        });
        cx.spawn(async move |this, cx| {
            let saved = load.await;
            let _ = this.update(cx, |this, cx| {
                for path in saved.into_iter().take(12) {
                    if !this.recent.contains(&path) {
                        this.recent.push(path);
                    }
                }
                this.recent.truncate(12);
                cx.notify();
            });
        })
        .detach();
        this.connect(cx);
        this
    }

    fn connect(&mut self, cx: &mut Context<Self>) {
        if self.connecting || self.closing {
            return;
        }
        self.ready = false;
        self.connecting = true;
        self.error = None;
        self.requests.clear();
        let start = cx
            .background_executor()
            .spawn(async { Client::start(&core_path()) });
        cx.spawn(async move |this, cx| {
            let result = start.await;
            let _ = this.update(cx, |this, cx| {
                this.connecting = false;
                if this.closing {
                    return;
                }
                match result {
                    Ok(client) => {
                        this.client = Some(client);
                        this.send(
                            "system.handshake",
                            json!({"versions":[1]}),
                            Pending::Handshake,
                        );
                    }
                    Err(error) => this.error = Some(error),
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn close(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.closing {
            return;
        }
        self.closing = true;
        let Some(client) = self.client.take() else {
            window.remove_window();
            return;
        };
        let shutdown = cx
            .background_executor()
            .spawn(async move { client.shutdown() });
        cx.spawn_in(window, async move |_this, cx| {
            let _ = shutdown.await;
            let _ = cx.update(|window, _| window.remove_window());
        })
        .detach();
        cx.notify();
    }

    pub(crate) fn workspace(&self) -> Option<&Workspace> {
        let id = self.active.as_ref()?;
        self.workspaces.iter().find(|workspace| &workspace.id == id)
    }

    pub fn selected_index(&self) -> Option<usize> {
        let name = self.target.as_ref()?;
        self.workspace()?
            .targets
            .iter()
            .position(|target| &target.id == name)
    }

    fn selected_target(&self) -> Option<&Target> {
        self.workspace()?.targets.get(self.selected_index()?)
    }

    fn job(&self) -> Option<&Job> {
        self.jobs
            .get(self.selected_builds.get(self.active.as_ref()?)?)
    }

    fn active_build(&self) -> Option<&Build> {
        let id = self.active.as_deref()?;
        self.jobs
            .values()
            .filter_map(|job| job.state.as_ref())
            .find(|build| build.workspace_id == id && build.active())
    }

    fn send(&mut self, method: &str, params: Value, pending: Pending) {
        let result = self
            .client
            .as_mut()
            .ok_or("Core is disconnected".to_string())
            .and_then(|client| client.send(method, params));
        match result {
            Ok(id) => {
                self.requests.insert(id, (Instant::now(), pending));
            }
            Err(error) => self.error = Some(error),
        }
    }

    fn open(&mut self, path: String, activate: bool) {
        if !self.ready {
            return;
        }
        if activate {
            self.open_token += 1;
        }
        self.error = None;
        self.input = Input::None;
        self.send(
            "workspace.open",
            json!({"path":path}),
            Pending::Open {
                activate,
                token: self.open_token,
            },
        );
    }

    fn pick(&mut self, cx: &mut Context<Self>) {
        if !self.ready {
            return;
        }
        let picked = cx.prompt_for_paths(PathPromptOptions {
            files: false,
            directories: true,
            multiple: false,
            prompt: Some("Open Trestle workspace".into()),
        });
        cx.spawn(async move |this, cx| {
            let picked = picked.await;
            let _ = this.update(cx, |this, cx| {
                match picked {
                    Ok(Ok(Some(paths))) => {
                        if let Some(path) = paths.first() {
                            if let Some(path) = path.to_str() {
                                this.open(path.to_owned(), true);
                            } else {
                                this.error = Some("Workspace path is not valid Unicode".into());
                            }
                        }
                    }
                    Ok(Err(error)) => {
                        this.error = Some(format!("File picker unavailable: {error}"));
                        this.input = Input::Path;
                    }
                    _ => {}
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn select_workspace(&mut self, id: String) {
        self.open_token += 1;
        if self.active.as_ref() == Some(&id) {
            return;
        }
        self.active = Some(id);
        self.target = None;
        self.project_filter = None;
        self.generated = None;
        self.detected = None;
        self.analysis = None;
        self.artifact_times.clear();
        self.graph_measured_only = false;
        self.comparison = None;
        self.history.clear();
        self.query.clear();
        self.profile.clear();
        self.relayout();
        self.get_history();
        self.detect();
        if let Some(build) = self
            .job()
            .and_then(|job| job.state.as_ref())
            .filter(|state| !state.active())
        {
            self.show_analysis(build.build_id.clone());
        }
    }

    fn remember(&mut self, path: String, cx: &mut Context<Self>) {
        self.recent.retain(|item| item != &path);
        self.recent.insert(0, path);
        self.recent.truncate(12);
        let recent = self.recent.clone();
        cx.background_executor()
            .spawn(async move {
                if let Some(file) = recent_path() {
                    let result = (|| -> std::io::Result<()> {
                        if let Some(parent) = file.parent() {
                            std::fs::create_dir_all(parent)?;
                        }
                        let temporary = file.with_extension(format!("{}.tmp", std::process::id()));
                        std::fs::write(&temporary, serde_json::to_vec(&recent)?)?;
                        std::fs::rename(temporary, file)
                    })();
                    if let Err(error) = result {
                        eprintln!("Cannot persist recent workspaces: {error}");
                    }
                }
            })
            .detach();
    }

    fn relayout(&mut self) {
        let result = self.workspace().map(|ws| Graph::layout(&ws.targets));
        self.graph = None;
        self.graph_error = None;
        match result {
            Some(Ok(graph)) => self.graph = Some(Rc::new(graph)),
            Some(Err(error)) => self.graph_error = Some(error),
            None => {}
        }
        self.graph_x = 0.;
        self.graph_y = 0.;
        self.graph_collapsed.clear();
        self.graph_expanded = None;
        self.refresh_related();
    }

    pub fn refresh_related(&mut self) {
        let result = self
            .graph
            .as_ref()
            .zip(self.selected_index())
            .map(|(graph, index)| {
                graph.related(
                    index,
                    self.direction == Direction::Reverse,
                    self.direction != Direction::Direct,
                )
            })
            .unwrap_or_default();
        self.graph_related = Rc::new(result);
    }

    fn find_graph_target(&mut self) {
        let query = self.query.to_lowercase();
        if query.is_empty() {
            return;
        }
        let Some(workspace) = self.workspace() else {
            return;
        };
        let Some((index, target)) = workspace
            .targets
            .iter()
            .enumerate()
            .find(|(_, target)| target.id.eq_ignore_ascii_case(&query))
            .or_else(|| {
                workspace
                    .targets
                    .iter()
                    .enumerate()
                    .find(|(_, target)| target.id.to_lowercase().contains(&query))
            })
        else {
            return;
        };
        let Some(graph) = &self.graph else { return };
        let node = graph.nodes[index];
        self.target = Some(target.id.clone());
        if self
            .graph_expanded
            .as_ref()
            .is_some_and(|nodes| !nodes[index])
        {
            self.graph_collapsed.clear();
            self.graph_expanded = None;
        }
        self.graph_measured_only = false;
        self.graph_x = -(node.layer as f32 * crate::graph_view::SLOT_X * self.zoom);
        self.graph_y = -(node.row as f32 * crate::graph_view::SLOT_Y * self.zoom);
        self.refresh_related();
    }

    pub fn toggle_branch(&mut self, index: usize) {
        let Some(graph) = &self.graph else { return };
        if !graph.can_collapse(index) {
            return;
        }
        if !self.graph_collapsed.remove(&index) {
            self.graph_collapsed.insert(index);
        }
        self.graph_expanded = (!self.graph_collapsed.is_empty())
            .then(|| Rc::new(graph.expanded_nodes(&self.graph_collapsed)));
    }

    pub fn select_target(&mut self, id: String) {
        self.target = if self.target.as_ref() == Some(&id) {
            None
        } else {
            Some(id)
        };
        self.refresh_related();
        if self.page == Page::Toolchains {
            self.detect();
        }
    }

    fn set_project(&mut self, member: Option<String>) {
        self.navigate(Page::Targets);
        self.project_filter = member;
        self.target = None;
        self.generated = None;
        self.refresh_related();
    }

    fn request_commands(&mut self, offset: u64) {
        let Some(build) = self.job().and_then(|job| job.state.as_ref()) else {
            return;
        };
        let Some(target) = self.target.clone() else {
            return;
        };
        let build = build.build_id.clone();
        if self.requests.values().any(|(_, kind)| {
            matches!(kind, Pending::TargetCommands(active_build, active_target) if active_build == &build && active_target == &target)
        }) { return; }
        self.send(
            "build.commands",
            json!({"build_id":build,"target":target,"offset":offset}),
            Pending::TargetCommands(build, target),
        );
    }

    fn detect(&mut self) {
        let Some(id) = self.active.clone() else {
            return;
        };
        let member = self
            .selected_target()
            .map(|target| target.member.clone())
            .unwrap_or_default();
        self.send(
            "toolchain.detect",
            json!({"workspace_id":id,"member":member}),
            Pending::Detect(id, member),
        );
    }

    fn get_history(&mut self) {
        if let Some(id) = self.active.clone() {
            self.send(
                "build.history",
                json!({"workspace_id":id}),
                Pending::History(id),
            );
        }
    }

    fn show_analysis(&mut self, id: String) {
        self.send(
            "analysis.summary",
            json!({"build_id":id}),
            Pending::Analysis(id),
        );
    }

    fn navigate(&mut self, page: Page) {
        self.page = page;
        self.input = Input::None;
        self.query.clear();
        if page == Page::Toolchains && self.ready {
            self.send("toolchain.list", json!({}), Pending::Inventory);
            self.detect();
        }
    }

    fn start(&mut self, configure: bool, repeat: bool, rebuild: bool) {
        let Some(id) = self.active.clone() else {
            return;
        };
        if !self.ready
            || self.active_build().is_some()
            || self
                .requests
                .values()
                .any(|(_, kind)| matches!(kind, Pending::Start(ws) if ws == &id))
        {
            return;
        }
        self.error = None;
        let selection = if repeat {
            self.job()
                .and_then(|job| job.state.as_ref())
                .map(|build| build.options.clone())
        } else {
            None
        };
        let (member, targets, profile) = match selection {
            Some(options) => (options.member, options.targets, options.profile),
            None => {
                let (member, targets) = self
                    .selected_target()
                    .map(|target| (target.member.clone(), vec![target.id.clone()]))
                    .unwrap_or_else(|| {
                        (self.project_filter.clone().unwrap_or_default(), Vec::new())
                    });
                (member, targets, self.profile.clone())
            }
        };
        self.drawer = Drawer::Output;
        self.query.clear();
        self.follow = true;
        self.send(
            if configure {
                "build.configure"
            } else {
                "build.start"
            },
            json!({"workspace_id":id,"member":member,"targets":targets,"profile":profile,"rebuild":rebuild}),
            Pending::Start(id),
        );
    }

    fn cancel(&mut self) {
        if let Some(id) = self.active_build().map(|build| build.build_id.clone()) {
            self.send("build.cancel", json!({"build_id":id}), Pending::Cancel);
        }
    }

    fn tick(&mut self, cx: &mut Context<Self>) {
        let mut changed = false;
        let messages = self.client.as_ref().map(Client::drain).unwrap_or_default();
        for message in messages {
            changed = true;
            match message {
                Message::Disconnected(error) => {
                    self.error = Some(error);
                    self.ready = false;
                    self.client.take();
                    self.requests.clear();
                    for job in self.jobs.values_mut() {
                        if let Some(state) = job.state.as_mut()
                            && state.active()
                        {
                            state.status = "disconnected".into();
                        }
                    }
                    break;
                }
                Message::Response { id, result } => {
                    let Some((_, kind)) = self.requests.remove(&id) else {
                        continue;
                    };
                    let handshake = matches!(kind, Pending::Handshake);
                    let result = result
                        .map_err(|error| error.message)
                        .and_then(|value| self.response(kind, value, cx));
                    if let Err(error) = result {
                        if handshake {
                            self.client.take();
                        }
                        self.error = Some(error);
                    }
                }
                Message::Event { method, event } => {
                    // Requests can take priority over already queued events. Apply
                    // statuses via polling and use notifications to wake the poll.
                    let Some(job) = self.jobs.get_mut(&event.build_id) else {
                        continue;
                    };
                    if event.sequence > job.sequence {
                        job.sequence = event.sequence;
                        job.needs_poll |= method == "event.build.status"
                            || method == "event.build.progress"
                            || event.data["next_offset"]
                                .as_u64()
                                .is_some_and(|offset| offset > job.output.offset);
                    }
                }
            }
        }
        let mut timed_out = Vec::new();
        self.requests.retain(|id, (when, pending)| {
            let expired = when.elapsed() > Duration::from_secs(35);
            if expired {
                timed_out.push((*id, matches!(pending, Pending::Handshake)));
            }
            !expired
        });
        if !timed_out.is_empty() {
            self.error =
                Some("A Core request timed out. Refresh the workspace or reconnect.".into());
            if timed_out.iter().any(|(_, handshake)| *handshake) {
                self.client.take();
            }
            changed = true;
        }
        if self.ready && self.last_poll.elapsed() >= Duration::from_millis(180) {
            self.last_poll = Instant::now();
            let selected = self
                .active
                .as_ref()
                .and_then(|id| self.selected_builds.get(id))
                .cloned();
            let poll: Vec<_> = self
                .jobs
                .iter()
                .filter_map(|(id, job)| {
                    let active = job.state.as_ref().is_some_and(Build::active);
                    (active || (job.needs_poll && selected.as_ref() == Some(id)))
                        .then_some(id.clone())
                })
                .collect();
            for id in poll {
                if !self
                    .requests
                    .values()
                    .any(|(_, pending)| matches!(pending, Pending::Status(build) if build == &id))
                {
                    self.send("build.status", json!({"build_id":id}), Pending::Status(id));
                }
            }
            if let Some(id) = selected {
                let job = self.jobs.get(&id);
                if let Some(job) = job
                    && job
                        .state
                        .as_ref()
                        .is_some_and(|state| job.output.offset < state.log_size)
                    && !self
                        .requests
                        .values()
                        .any(|(_, pending)| matches!(pending, Pending::Log(build) if build == &id))
                {
                    self.send(
                        "build.log",
                        json!({"build_id":id,"offset":job.output.offset}),
                        Pending::Log(id),
                    );
                }
            }
        }
        if changed {
            cx.notify();
        }
    }

    fn response(
        &mut self,
        kind: Pending,
        value: Value,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        match kind {
            Pending::Handshake => {
                if value["version"] != 1 {
                    return Err("Incompatible Core protocol".into());
                }
                self.ready = true;
                let reconnect: Vec<_> = self.workspaces.iter().map(|ws| ws.path.clone()).collect();
                self.selected_builds.clear();
                self.jobs.clear();
                self.workspaces.clear();
                self.active = None;
                for path in reconnect.into_iter().take(12) {
                    self.open(path, false);
                }
                if let Some(path) = self.initial.take() {
                    let path = path.to_str().ok_or("Workspace path is not valid Unicode")?;
                    self.open(path.to_owned(), true);
                }
            }
            Pending::Open { activate, token } => {
                let ws: Workspace = decode(value)?;
                let id = ws.id.clone();
                let path = ws.path.clone();
                self.upsert(ws);
                if (activate && token == self.open_token) || (!activate && self.active.is_none()) {
                    self.select_workspace(id);
                    self.remember(path, cx);
                }
            }
            Pending::Update => {
                let ws: Workspace = decode(value)?;
                let current = self.active.as_ref() == Some(&ws.id);
                self.upsert(ws);
                if current {
                    self.relayout();
                    self.detect();
                }
            }
            Pending::Start(ws) => {
                let state: Build = decode(value)?;
                let id = state.build_id.clone();
                self.selected_builds.insert(ws, id.clone());
                self.jobs.insert(
                    id,
                    Job {
                        state: Some(state),
                        needs_poll: true,
                        ..Default::default()
                    },
                );
                self.build_at = Some(Instant::now());
                self.generated = None;
                self.analysis = None;
                self.artifact_times.clear();
                self.graph_measured_only = false;
                self.comparison = None;
                self.prune_jobs();
            }
            Pending::Status(id) => {
                let state: Build = decode(value)?;
                let job = self.jobs.entry(id.clone()).or_default();
                let ended = !state.active() && job.state.as_ref().is_some_and(Build::active);
                job.state = Some(state);
                job.needs_poll = false;
                if ended {
                    self.get_history();
                    if self
                        .active
                        .as_ref()
                        .and_then(|ws| self.selected_builds.get(ws))
                        == Some(&id)
                    {
                        self.show_analysis(id);
                    }
                }
            }
            Pending::Cancel => {}
            Pending::Log(id) => {
                let page: LogPage = decode(value)?;
                if let Some(job) = self.jobs.get_mut(&id) {
                    let now_complete = job
                        .state
                        .as_ref()
                        .is_some_and(|state| !state.active() && page.next_offset >= state.log_size);
                    job.output.append(page, now_complete);
                    if self.follow && !job.output.lines.is_empty() {
                        self.log_scroll
                            .scroll_to_item(job.output.lines.len() - 1, ScrollStrategy::Bottom);
                    }
                }
            }
            Pending::History(id) => {
                if self.active.as_ref() == Some(&id) {
                    self.history = decode(value)?;
                }
            }
            Pending::Analysis(id) => {
                let analysis: Analysis = decode(value)?;
                let current = self
                    .active
                    .as_ref()
                    .and_then(|ws| self.selected_builds.get(ws))
                    == Some(&id);
                if current {
                    self.artifact_times = analysis
                        .timings
                        .iter()
                        .filter(|timing| !timing.target.is_empty())
                        .map(|timing| (timing.target.clone(), timing.end_ms - timing.start_ms))
                        .collect();
                    self.analysis = Some(analysis);
                } else if self.history.iter().any(|build| build.build_id == id) {
                    self.comparison = Some(analysis);
                }
            }
            Pending::TargetCommands(build, target) => {
                let active = self
                    .job()
                    .and_then(|job| job.state.as_ref())
                    .map(|state| &state.build_id);
                if active == Some(&build) && self.target.as_ref() == Some(&target) {
                    self.generated = Some((build, target, decode(value)?));
                }
            }
            Pending::Inventory => self.inventory = decode(value)?,
            Pending::Detect(id, member) => {
                let selected = self
                    .selected_target()
                    .map_or("", |target| target.member.as_str());
                if self.active.as_ref() == Some(&id) && selected == member {
                    self.detected = Some(decode(value)?);
                }
            }
        }
        Ok(())
    }

    fn upsert(&mut self, ws: Workspace) {
        if let Some(existing) = self.workspaces.iter_mut().find(|item| item.id == ws.id) {
            *existing = ws;
        } else {
            self.workspaces.push(ws);
        }
    }

    fn prune_jobs(&mut self) {
        if self.jobs.len() <= 24 {
            return;
        }
        if let Some(oldest) = self
            .jobs
            .keys()
            .filter(|&id| {
                self.jobs[id]
                    .state
                    .as_ref()
                    .is_none_or(|state| !state.active())
                    && !self.selected_builds.values().any(|selected| selected == id)
            })
            .min()
            .cloned()
        {
            self.jobs.remove(&oldest);
        }
    }
}

fn decode<T: DeserializeOwned>(value: Value) -> Result<T, String> {
    serde_json::from_value(value).map_err(|error| format!("Core returned unexpected data: {error}"))
}

fn recent_path() -> Option<PathBuf> {
    desktop_state_dir().map(|root| root.join("recent.json"))
}
