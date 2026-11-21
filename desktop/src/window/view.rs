use super::*;
use crate::graph_view;
use gpui::prelude::*;

#[derive(Clone, Copy)]
pub struct Theme {
    pub background: Rgba,
    pub panel: Rgba,
    pub surface: Rgba,
    pub hover: Rgba,
    pub border: Rgba,
    pub text: Rgba,
    pub muted: Rgba,
    pub accent: Rgba,
    pub accent_bg: Rgba,
    pub error: Rgba,
    pub warning: Rgba,
}

impl Theme {
    fn new(light: bool) -> Self {
        let [
            background,
            panel,
            surface,
            hover,
            border,
            text,
            muted,
            accent,
            accent_bg,
            error,
            warning,
        ] = if light {
            [
                0xf0f3f3, 0xfafcfc, 0xe8ecec, 0xdce6e3, 0xcdd5d4, 0x1e2929, 0x546463, 0x176e59,
                0xd1eae2, 0xa23337, 0x895800,
            ]
        } else {
            [
                0x101415, 0x151a1b, 0x1d2425, 0x253131, 0x2c3738, 0xe6eeee, 0xa5b5b2, 0x88e1c0,
                0x204137, 0xff9b9a, 0xf2c884,
            ]
        }
        .map(rgb);
        Self {
            background,
            panel,
            surface,
            hover,
            border,
            text,
            muted,
            accent,
            accent_bg,
            error,
            warning,
        }
    }
}

pub fn control(
    id: impl Into<ElementId>,
    label: impl Into<SharedString>,
    theme: Theme,
    active: bool,
) -> Stateful<Div> {
    div()
        .id(id)
        .tab_index(0)
        .border_1()
        .border_color(gpui::transparent_black())
        .focus(move |style| style.border_color(theme.accent))
        .px_2()
        .py_1()
        .rounded_md()
        .cursor_pointer()
        .text_color(if active { theme.accent } else { theme.text })
        .bg(if active {
            theme.accent_bg
        } else {
            theme.surface
        })
        .hover(move |s| s.bg(theme.hover))
        .child(label.into())
}

fn label(text: impl Into<SharedString>, theme: Theme) -> Div {
    div().text_xs().text_color(theme.muted).child(text.into())
}

fn title(text: impl Into<SharedString>) -> Div {
    div().font_weight(FontWeight::SEMIBOLD).child(text.into())
}

impl Render for Desktop {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if std::mem::take(&mut self.focus_input) {
            self.input_focus.focus(window);
        }
        let theme = Theme::new(self.light);
        let viewport = window.viewport_size();
        let compact = f32::from(viewport.width) < 1050.;
        let drawer_height = self
            .drawer_height
            .min(f32::from(viewport.height) * 0.53)
            .max(118.);
        let header = self.header(theme, cx);
        let tabs = self.tabs(theme, cx);
        let page = self.page_content(theme, compact, viewport, cx);
        let inspector = self.inspector(theme, cx);
        let explorer = self.explorer(theme, cx);
        let bottom = self.bottom(theme, cx);

        div()
            .id("desktop")
            .track_focus(&self.focus)
            .size_full()
            .flex()
            .flex_col()
            .overflow_hidden()
            .bg(theme.background)
            .text_color(theme.text)
            .text_sm()
            .on_action(cx.listener(|this, _: &OpenWorkspace, _, cx| this.pick(cx)))
            .on_action(cx.listener(|this, _: &BuildSelected, _, cx| {
                this.start(false, false, false);
                cx.notify();
            }))
            .on_action(cx.listener(|this, _: &Commands, _, cx| {
                this.commands = !this.commands;
                cx.notify();
            }))
            .on_action(cx.listener(|this, _: &Search, _, cx| {
                this.show_input(Input::Find);
                cx.notify();
            }))
            .on_action(cx.listener(|this, _: &CloseWindow, window, cx| this.close(window, cx)))
            .on_action(|_: &TabNext, window, _| window.focus_next())
            .on_action(|_: &TabPrevious, window, _| window.focus_prev())
            .on_key_down(cx.listener(|this, event: &KeyDownEvent, window, cx| {
                this.input_key(event, window, cx)
            }))
            .on_mouse_move(cx.listener(|this, event: &MouseMoveEvent, window, cx| {
                if let Some((at, height)) = this.resize_at {
                    let max = f32::from(window.viewport_size().height) * 0.53;
                    this.drawer_height =
                        (height + f32::from(at - event.position.y)).clamp(118., max.max(118.));
                    cx.notify();
                }
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, _, _, _| {
                    this.resize_at = None;
                    this.graph_drag = None;
                }),
            )
            .on_mouse_up_out(
                MouseButton::Left,
                cx.listener(|this, _, _, _| {
                    this.resize_at = None;
                    this.graph_drag = None;
                }),
            )
            .child(header)
            .child(tabs)
            .when(self.commands, |root| {
                root.child(self.command_bar(theme, cx))
            })
            .when(self.input != Input::None, |root| {
                root.child(self.input_bar(theme, cx))
            })
            .when_some(self.error.clone(), |root, error| {
                root.child(
                    div()
                        .flex()
                        .gap_2()
                        .items_center()
                        .px_3()
                        .py_1()
                        .bg(theme.surface)
                        .text_color(theme.error)
                        .child(div().flex_1().child(error))
                        .child(control("dismiss-error", "Dismiss", theme, false).on_click(
                            cx.listener(|this, _, _, cx| {
                                this.error = None;
                                cx.notify();
                            }),
                        )),
                )
            })
            .child(
                div()
                    .flex()
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .child(explorer.w(px(if compact { 174. } else { 216. })))
                    .child(page)
                    .when(
                        !compact && self.workspace().is_some() && self.page != Page::Settings,
                        |row| row.child(inspector),
                    ),
            )
            .child(
                div()
                    .id("drawer-resize")
                    .h(px(5.))
                    .flex_shrink_0()
                    .bg(theme.border)
                    .cursor_row_resize()
                    .hover(move |s| s.bg(theme.accent))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, e: &MouseDownEvent, _, _| {
                            this.resize_at = Some((e.position.y, drawer_height));
                        }),
                    ),
            )
            .child(bottom.h(px(drawer_height)))
    }
}

impl Desktop {
    fn header(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        let build = self.job().and_then(|job| job.state.as_ref());
        let active = self.active_build().is_some();
        div()
            .flex()
            .items_center()
            .gap_2()
            .px_3()
            .h(px(52.))
            .flex_shrink_0()
            .border_b_1()
            .border_color(theme.border)
            .bg(theme.panel)
            .child(
                div()
                    .w(px(22.))
                    .h(px(22.))
                    .rounded_md()
                    .bg(theme.accent_bg)
                    .flex()
                    .items_center()
                    .justify_center()
                    .font_weight(FontWeight::BOLD)
                    .text_color(theme.accent)
                    .child("T"),
            )
            .child(title("Trestle"))
            .child(
                control("open-workspace", "Open", theme, false)
                    .on_click(cx.listener(|this, _, _, cx| this.pick(cx))),
            )
            .child(
                control("commands", "Commands", theme, self.commands).on_click(cx.listener(
                    |this, _, _, cx| {
                        this.commands = !this.commands;
                        cx.notify();
                    },
                )),
            )
            .child(div().flex_1())
            .when(self.workspace().is_some(), |bar| {
                bar.child(
                    control(
                        "profile",
                        format!(
                            "Profile · {}",
                            if self.profile.is_empty() {
                                "Default"
                            } else {
                                &self.profile
                            }
                        ),
                        theme,
                        false,
                    )
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.profile = match this.profile.as_str() {
                            "debug" => "release",
                            "release" => "",
                            _ => "debug",
                        }
                        .into();
                        cx.notify();
                    })),
                )
                .child(
                    control("configure", "Configure", theme, false).on_click(cx.listener(
                        |this, _, _, cx| {
                            this.start(true, false, false);
                            cx.notify();
                        },
                    )),
                )
                .when(build.is_some() && !active, |bar| {
                    bar.child(
                        control("rerun", "Run again", theme, false).on_click(cx.listener(
                            |this, _, _, cx| {
                                this.start(false, true, false);
                                cx.notify();
                            },
                        )),
                    )
                })
                .child(if active {
                    control("cancel", "Cancel", theme, false)
                        .text_color(theme.warning)
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.cancel();
                            cx.notify();
                        }))
                } else {
                    control("build", "Build", theme, true).on_click(cx.listener(
                        |this, _, _, cx| {
                            this.start(false, false, false);
                            cx.notify();
                        },
                    ))
                })
            })
            .child(label(
                if self.closing {
                    "Closing"
                } else if self.ready {
                    "Core connected"
                } else if self.connecting || self.client.is_some() {
                    "Connecting…"
                } else {
                    "Disconnected"
                },
                theme,
            ))
            .when(
                !self.ready && !self.connecting && self.client.is_none() && !self.closing,
                |bar| {
                    bar.child(
                        control("reconnect", "Reconnect", theme, false)
                            .on_click(cx.listener(|this, _, _, cx| this.connect(cx))),
                    )
                },
            )
    }

    fn tabs(&self, theme: Theme, cx: &mut Context<Self>) -> Stateful<Div> {
        div()
            .id("workspaces")
            .flex()
            .items_center()
            .gap_1()
            .px_3()
            .py_1()
            .h(px(40.))
            .flex_shrink_0()
            .overflow_x_scroll()
            .border_b_1()
            .border_color(theme.border)
            .child(label("Workspaces", theme).mr_2())
            .children(self.workspaces.iter().map(|ws| {
                let id = ws.id.clone();
                control(
                    SharedString::from(format!("ws-{}", ws.id)),
                    ws.name.clone(),
                    theme,
                    self.active.as_ref() == Some(&ws.id),
                )
                .flex_shrink_0()
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.select_workspace(id.clone());
                    cx.notify();
                }))
            }))
    }

    fn command_bar(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        div()
            .flex()
            .flex_wrap()
            .items_center()
            .gap_2()
            .p_2()
            .bg(theme.panel)
            .border_b_1()
            .border_color(theme.border)
            .child(
                control("command-open", "Open workspace", theme, false)
                    .on_click(cx.listener(|this, _, _, cx| this.pick(cx))),
            )
            .child(
                control("command-path", "Open path…", theme, false).on_click(cx.listener(
                    |this, _, _, cx| {
                        this.commands = false;
                        this.path_input.clear();
                        this.show_input(Input::Path);
                        cx.notify();
                    },
                )),
            )
            .child(
                control("command-rebuild", "Clean and rebuild", theme, false).on_click(
                    cx.listener(|this, _, _, cx| {
                        this.commands = false;
                        this.start(false, false, true);
                        cx.notify();
                    }),
                ),
            )
            .child(
                control("command-refresh", "Refresh workspace", theme, false).on_click(
                    cx.listener(|this, _, _, cx| {
                        if let Some(id) = this.active.clone() {
                            this.send("workspace.get", json!({"workspace_id":id}), Pending::Update);
                        }
                        this.commands = false;
                        cx.notify();
                    }),
                ),
            )
            .child(
                control("command-theme", "Switch theme", theme, false).on_click(cx.listener(
                    |this, _, _, cx| {
                        this.light = !this.light;
                        cx.notify();
                    },
                )),
            )
    }

    fn input_bar(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        let hint = if self.input == Input::Path {
            "Workspace path"
        } else {
            "Find in current view"
        };
        div()
            .flex()
            .items_center()
            .gap_2()
            .p_2()
            .bg(theme.panel)
            .border_b_1()
            .border_color(theme.border)
            .child(label(hint, theme))
            .child(self.entry_surface(theme, cx))
            .when(self.input == Input::Path, |bar| {
                bar.child(
                    control("submit-path", "Open", theme, true).on_click(cx.listener(
                        |this, _, _, cx| {
                            this.open(this.path_input.trim().to_owned(), true);
                            cx.notify();
                        },
                    )),
                )
            })
            .child(
                control("close-input", "Esc", theme, false).on_click(cx.listener(
                    |this, _, _, cx| {
                        this.input = Input::None;
                        this.query.clear();
                        cx.notify();
                    },
                )),
            )
    }

    fn input_key(&mut self, event: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let key = &event.keystroke;
        if key.key == "escape" {
            self.input = Input::None;
            self.query.clear();
            self.commands = false;
            self.focus.focus(window);
            cx.stop_propagation();
            cx.notify();
            return;
        }
        if self.input == Input::None || !self.input_focus.is_focused(window) {
            return;
        }
        if key.key == "enter" {
            if self.input == Input::Path {
                self.open(self.path_input.trim().to_owned(), true);
                self.focus.focus(window);
            } else if self.page == Page::Graph {
                self.find_graph_target();
            }
            cx.stop_propagation();
            cx.notify();
            return;
        }
        self.edit_input_key(event, cx);
    }

    fn explorer(&self, theme: Theme, cx: &mut Context<Self>) -> Stateful<Div> {
        let mut list = div()
            .id("explorer")
            .flex()
            .flex_col()
            .flex_shrink_0()
            .gap_2()
            .p_3()
            .overflow_y_scroll()
            .bg(theme.panel)
            .border_r_1()
            .border_color(theme.border);
        for (name, page) in [
            ("Targets", Page::Targets),
            ("Dependency graph", Page::Graph),
            ("Toolchains", Page::Toolchains),
            ("Settings", Page::Settings),
        ] {
            list = list.child(
                control(name, name, theme, self.page == page).on_click(cx.listener(
                    move |this, _, _, cx| {
                        this.navigate(page);
                        cx.notify();
                    },
                )),
            );
        }
        if let Some(ws) = self.workspace() {
            list = list.child(label("PROJECTS", theme).mt_3()).child(
                div()
                    .text_xs()
                    .text_color(theme.muted)
                    .child(ws.path.clone()),
            );
            if ws.projects.len() > 1 {
                list = list.child(
                    control(
                        "all-projects",
                        "All projects",
                        theme,
                        self.page == Page::Targets && self.project_filter.is_none(),
                    )
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.set_project(None);
                        cx.notify();
                    })),
                );
            }
            for project in &ws.projects {
                let member = project.name.clone();
                let name = if member.is_empty() {
                    ws.name.clone()
                } else {
                    member.clone()
                };
                let active =
                    self.page == Page::Targets && self.project_filter.as_ref() == Some(&member);
                list = list.child(
                    control(
                        SharedString::from(format!("project-{}", member)),
                        name,
                        theme,
                        active,
                    )
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.set_project(Some(member.clone()));
                        cx.notify();
                    }))
                    .child(label(
                        format!("{} · {}", project.profile, selector(&project.compiler)),
                        theme,
                    ))
                    .child(label(project.path.clone(), theme).text_ellipsis()),
                );
            }
        }
        if self
            .recent
            .iter()
            .any(|path| !self.workspaces.iter().any(|ws| &ws.path == path))
        {
            list = list.child(label("RECENT WORKSPACES", theme).mt_3());
            for path in self
                .recent
                .iter()
                .filter(|path| !self.workspaces.iter().any(|ws| &ws.path == *path))
            {
                let path = path.clone();
                list = list.child(
                    control(SharedString::from(path.clone()), path.clone(), theme, false)
                        .text_xs()
                        .text_ellipsis()
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.open(path.clone(), true);
                            cx.notify();
                        })),
                );
            }
        }
        list
    }

    fn page_content(
        &self,
        theme: Theme,
        compact: bool,
        viewport: Size<Pixels>,
        cx: &mut Context<Self>,
    ) -> Div {
        let mut base = div().flex_1().min_w_0().flex().flex_col().overflow_hidden();
        if self.page == Page::Settings {
            return base.child(
                div()
                    .p_4()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .items_start()
                    .child(title("Settings").text_xl())
                    .child(label("Appearance", theme))
                    .child(
                        control(
                            "theme",
                            if self.light {
                                "Use dark theme"
                            } else {
                                "Use light theme"
                            },
                            theme,
                            true,
                        )
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.light = !this.light;
                            cx.notify();
                        })),
                    )
                    .child(label("Keyboard shortcuts", theme))
                    .children(
                        [
                            "Open  Ctrl/⌘ O",
                            "Build  Ctrl/⌘ B",
                            "Commands  Ctrl/⌘ K",
                            "Find  Ctrl/⌘ F",
                            "Close  Ctrl/⌘ W",
                        ]
                        .map(|text| div().child(text)),
                    ),
            );
        }
        let Some(ws) = self.workspace() else {
            let pending = self
                .requests
                .values()
                .any(|(_, kind)| matches!(kind, Pending::Open { .. }));
            return base.child(
                div()
                    .flex()
                    .flex_col()
                    .flex_1()
                    .items_center()
                    .justify_center()
                    .gap_3()
                    .child(
                        title(if pending {
                            "Opening workspace…"
                        } else {
                            "Open a Trestle workspace"
                        })
                        .text_xl(),
                    )
                    .child(label("Select a directory containing trestle.toml.", theme))
                    .child(
                        control("open-empty", "Open workspace", theme, true)
                            .on_click(cx.listener(|this, _, _, cx| this.pick(cx))),
                    )
                    .child(control("path-empty", "Enter path…", theme, false).on_click(
                        cx.listener(|this, _, _, cx| {
                            this.show_input(Input::Path);
                            cx.notify();
                        }),
                    )),
            );
        };
        let heading = |name, description: String| {
            div()
                .flex()
                .items_center()
                .justify_between()
                .gap_2()
                .border_b_1()
                .border_color(theme.border)
                .px_4()
                .py_3()
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .child(title(name).text_lg())
                        .child(label(description, theme)),
                )
                .child(
                    control("find-page", "Find", theme, self.input == Input::Find).on_click(
                        cx.listener(|this, _, _, cx| {
                            this.show_input(Input::Find);
                            cx.notify();
                        }),
                    ),
                )
        };
        match self.page {
            Page::Targets => {
                let query = self.query.to_lowercase();
                let indices: Vec<_> = ws
                    .targets
                    .iter()
                    .enumerate()
                    .filter(|(_, target)| {
                        self.project_filter
                            .as_ref()
                            .is_none_or(|member| member == &target.member)
                            && (query.is_empty()
                                || target.id.to_lowercase().contains(&query)
                                || target.member.to_lowercase().contains(&query))
                    })
                    .map(|(index, _)| index)
                    .collect();
                base = base.child(heading(
                    "Targets",
                    format!("{} in {}", indices.len(), ws.name),
                ));
                if indices.is_empty() {
                    return base.child(label("No matching targets.", theme).p_4());
                }
                base.child(
                    uniform_list(
                        "targets",
                        indices.len(),
                        cx.processor(move |this, range: std::ops::Range<usize>, _, cx| {
                            let Some(ws) = this.workspace() else {
                                return Vec::new();
                            };
                            range
                                .filter_map(|row| ws.targets.get(indices[row]))
                                .map(|target| {
                                    let id = target.id.clone();
                                    let active = this.target.as_ref() == Some(&id);
                                    div()
                                        .id(SharedString::from(id.clone()))
                                        .w_full()
                                        .h(px(40.))
                                        .px_4()
                                        .flex()
                                        .items_center()
                                        .gap_3()
                                        .cursor_pointer()
                                        .border_b_1()
                                        .border_color(theme.border)
                                        .bg(if active {
                                            theme.accent_bg
                                        } else {
                                            theme.background
                                        })
                                        .hover(move |s| s.bg(theme.hover))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.select_target(id.clone());
                                            cx.notify();
                                        }))
                                        .child(
                                            div()
                                                .flex_1()
                                                .min_w_0()
                                                .text_ellipsis()
                                                .child(target.id.clone()),
                                        )
                                        .child(label(target.member.clone(), theme))
                                        .child(
                                            label(target.kind.clone(), theme)
                                                .px_2()
                                                .py_1()
                                                .rounded_md()
                                                .bg(theme.surface),
                                        )
                                })
                                .collect()
                        }),
                    )
                    .flex_1()
                    .w_full()
                    .min_h_0(),
                )
                .when(compact, |base| {
                    base.when_some(self.selected_target(), |base, target| {
                        base.child(div().border_t_1().border_color(theme.border).p_2().child(
                            label(
                                format!(
                                    "{} · {} sources · {}",
                                    target.id,
                                    target.sources.len(),
                                    selector(&target.compiler)
                                ),
                                theme,
                            ),
                        ))
                    })
                })
            }
            Page::Graph => base
                .child(heading(
                    "Dependency graph",
                    format!("{} targets · provider → consumer", ws.targets.len()),
                ))
                .child(graph_view::render(self, theme, viewport, cx)),
            Page::Toolchains => base
                .child(heading(
                    "Toolchains",
                    format!("Selected · {}", selector(&ws.compiler)),
                ))
                .child(self.toolchains(theme, cx)),
            Page::Settings => base,
        }
    }

    fn inspector(&self, theme: Theme, cx: &mut Context<Self>) -> Stateful<Div> {
        let mut panel = div()
            .id("inspector")
            .w(px(274.))
            .flex_shrink_0()
            .flex()
            .flex_col()
            .gap_2()
            .p_3()
            .overflow_y_scroll()
            .border_l_1()
            .border_color(theme.border)
            .bg(theme.panel)
            .child(title("Inspector"));
        if let Some(target) = self.selected_target() {
            panel = panel
                .child(title(target.id.clone()).text_base())
                .child(label(
                    format!("{} · {}", target.kind, selector(&target.compiler)),
                    theme,
                ))
                .child(label(
                    format!("Output name: {}", selector(&target.output_name)),
                    theme,
                ));
            if let Some(artifact) = self
                .job()
                .and_then(|job| job.state.as_ref())
                .and_then(|state| state.artifacts.get(&target.id))
            {
                panel = panel
                    .child(label("Planned artifact", theme))
                    .child(div().text_xs().child(artifact.clone()));
            }
            if let Some(build) = self.job().and_then(|job| job.state.as_ref()) {
                let page = self
                    .generated
                    .as_ref()
                    .filter(|(id, name, _)| id == &build.build_id && name == &target.id)
                    .map(|(_, _, page)| page);
                panel = panel.child(
                    control(
                        "generated-commands",
                        if page.is_some() {
                            "Refresh commands"
                        } else {
                            "Generated commands"
                        },
                        theme,
                        false,
                    )
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.request_commands(0);
                        cx.notify();
                    })),
                );
                if let Some(page) = page {
                    if page.available {
                        for command in &page.commands {
                            let args = serde_json::to_string(
                                &command.arguments.as_deref().unwrap_or_default(),
                            )
                            .unwrap_or_default();
                            panel = panel
                                .child(title(command.action.clone()).text_xs())
                                .child(label(command.directory.clone(), theme))
                                .child(div().text_xs().child(command.executable.clone()))
                                .child(div().text_xs().child(args))
                                .child(label(command.outputs.join(", "), theme));
                        }
                        if let Some(offset) = page.next_offset {
                            panel = panel.child(
                                control("next-commands", "Next commands", theme, false).on_click(
                                    cx.listener(move |this, _, _, cx| {
                                        this.request_commands(offset);
                                        cx.notify();
                                    }),
                                ),
                            );
                        }
                        if page.truncated {
                            panel = panel.child(label("Command retention limit reached.", theme));
                        }
                    } else {
                        panel = panel.child(label(
                            "Configure or build this target to inspect its commands.",
                            theme,
                        ));
                    }
                }
            }
            for (name, values) in [
                ("Sources", &target.sources),
                ("Dependencies", &target.dependencies),
                ("Packages", &target.packages),
                ("Compile options", &target.compile_flags),
                ("Link options", &target.link_flags),
            ] {
                if !values.is_empty() {
                    panel = panel.child(label(name, theme).mt_2()).children(
                        values
                            .iter()
                            .map(|text| div().text_xs().child(text.clone())),
                    );
                }
            }
        } else {
            panel = panel.child(label(
                "Select a target to inspect its configuration.",
                theme,
            ));
        }
        panel
    }

    fn toolchains(&self, theme: Theme, cx: &mut Context<Self>) -> Stateful<Div> {
        let mut list = div()
            .id("toolchain-list")
            .flex_1()
            .min_h_0()
            .p_3()
            .gap_2()
            .flex()
            .flex_col()
            .overflow_y_scroll();
        if let Some(tc) = &self.detected {
            list = list.child(
                div()
                    .p_2()
                    .bg(theme.accent_bg)
                    .rounded_md()
                    .child(title(format!("Configured · {} {}", tc.kind, tc.version)))
                    .child(label(
                        format!("{} · {} · {}", tc.target, tc.cc, tc.cxx),
                        theme,
                    )),
            );
        }
        let target_member = self
            .selected_target()
            .map(|t| t.member.clone())
            .unwrap_or_default();
        let query = self.query.to_lowercase();
        for tool in self.inventory.iter().filter(|tool| {
            query.is_empty()
                || tool.name.to_lowercase().contains(&query)
                || tool.family.to_lowercase().contains(&query)
        }) {
            let name = tool.name.clone();
            let path = tool.path.clone();
            let member = target_member.clone();
            let selectable = tool.ready
                && !path.is_empty()
                && tool.execution != "wsl"
                && matches!(
                    tool.family.to_ascii_lowercase().as_str(),
                    "gcc" | "clang" | "msvc" | "mingw"
                );
            list = list.child(
                div()
                    .flex()
                    .items_start()
                    .gap_2()
                    .border_b_1()
                    .border_color(theme.border)
                    .p_2()
                    .child(
                        div()
                            .flex_1()
                            .child(title(format!("{} · {}", name, tool.version)))
                            .child(label(
                                format!(
                                    "{} · {} · {}",
                                    tool.family,
                                    if tool.ready { "Ready" } else { "Unavailable" },
                                    selector(&tool.execution)
                                ),
                                theme,
                            ))
                            .child(label(
                                if tool.path.is_empty() {
                                    tool.detail.clone()
                                } else {
                                    tool.path.clone()
                                },
                                theme,
                            )),
                    )
                    .when(selectable, |row| {
                        row.child(
                            control(
                                SharedString::from(format!("select-{name}-{path}")),
                                "Use",
                                theme,
                                false,
                            )
                            .on_click(cx.listener(
                                move |this, _, _, cx| {
                                    if let Some(id) = this.active.clone() {
                                        this.send(
                                        "toolchain.select",
                                        json!({"workspace_id":id,"member":member,"compiler":path}),
                                        Pending::Update,
                                    );
                                    }
                                    cx.notify();
                                },
                            )),
                        )
                    }),
            );
        }
        if self.inventory.is_empty() {
            list = list.child(label(
                if self
                    .requests
                    .values()
                    .any(|(_, p)| matches!(p, Pending::Inventory))
                {
                    "Detecting components…"
                } else {
                    "No toolchain components available."
                },
                theme,
            ));
        }
        list
    }

    fn bottom(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        let mut tabs = div()
            .flex()
            .items_center()
            .gap_1()
            .px_2()
            .py_1()
            .border_b_1()
            .border_color(theme.border);
        for (name, drawer) in [
            ("Output", Drawer::Output),
            ("Problems", Drawer::Problems),
            ("History", Drawer::History),
            ("Analysis", Drawer::Analysis),
        ] {
            tabs = tabs.child(control(name, name, theme, self.drawer == drawer).on_click(
                cx.listener(move |this, _, _, cx| {
                    this.drawer = drawer;
                    this.query.clear();
                    if drawer == Drawer::History {
                        this.get_history();
                    }
                    cx.notify();
                }),
            ));
        }
        if let Some(build) = self.job().and_then(|job| job.state.as_ref()) {
            let detail = build
                .progress
                .as_ref()
                .map(|p| {
                    if build.active() {
                        format!(
                            "{} {}/{} · {} running",
                            p.member, p.completed, p.total, p.running
                        )
                    } else {
                        format!("{} {}/{}", p.member, p.completed, p.total)
                    }
                })
                .unwrap_or_else(|| {
                    if build.options.targets.is_empty() {
                        "Default targets".into()
                    } else {
                        build.options.targets.join(", ")
                    }
                });
            let elapsed = if build.active() {
                self.build_at
                    .map(|at| format!(" · {:.1}s", at.elapsed().as_secs_f32()))
                    .unwrap_or_default()
            } else {
                format!(" · {} ms", build.duration_ms)
            };
            tabs = tabs.child(div().flex_1()).child(
                label(format!("{} · {}{elapsed}", build.status, detail), theme).text_color(
                    if build.error.is_some() {
                        theme.error
                    } else {
                        theme.muted
                    },
                ),
            );
        }
        let content = match self.drawer {
            Drawer::Output | Drawer::Problems => self.logs(theme, cx),
            Drawer::History => self.history_view(theme, cx),
            Drawer::Analysis => self.analysis_view(theme, cx),
        };
        div()
            .flex()
            .flex_col()
            .flex_shrink_0()
            .overflow_hidden()
            .bg(theme.panel)
            .child(tabs)
            .child(content)
    }

    fn logs(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        let mut panel = div().flex().flex_col().flex_1().min_h_0();
        let Some(job) = self.job() else {
            return panel.child(label("Build output will appear here.", theme).p_3());
        };
        let problems = self.drawer == Drawer::Problems;
        let query = self.query.to_lowercase();
        let lines: Vec<_> = job
            .output
            .lines
            .iter()
            .enumerate()
            .filter(|(_, line)| {
                (!problems || problem(line).is_some())
                    && (query.is_empty() || line.to_lowercase().contains(&query))
            })
            .map(|(i, _)| i)
            .collect();
        let mut tools = div()
            .flex()
            .items_center()
            .gap_2()
            .px_2()
            .py_1()
            .child(
                control(
                    "follow",
                    if self.follow {
                        "Auto-scroll on"
                    } else {
                        "Auto-scroll paused"
                    },
                    theme,
                    self.follow,
                )
                .on_click(cx.listener(|this, _, _, cx| {
                    this.follow = !this.follow;
                    cx.notify();
                })),
            )
            .child(
                control("find-logs", "Filter", theme, self.input == Input::Find).on_click(
                    cx.listener(|this, _, _, cx| {
                        this.show_input(Input::Find);
                        cx.notify();
                    }),
                ),
            )
            .child(
                control("copy-logs", "Copy output", theme, false).on_click(cx.listener(
                    |this, _, _, cx| {
                        if let Some(job) = this.job() {
                            cx.write_to_clipboard(ClipboardItem::new_string(
                                job.output
                                    .lines
                                    .iter()
                                    .cloned()
                                    .collect::<Vec<_>>()
                                    .join("\n"),
                            ));
                        }
                    },
                )),
            );
        if let Some(state) = &job.state {
            if let Some(error) = &state.error {
                panel = panel.child(
                    div()
                        .px_2()
                        .py_1()
                        .text_color(theme.error)
                        .child(error.message.clone()),
                );
            }
            for (index, path) in state.log_paths.iter().enumerate() {
                let path = PathBuf::from(path);
                tools = tools.child(
                    control(
                        ("raw-log", index),
                        format!("Full log {}", index + 1),
                        theme,
                        false,
                    )
                    .on_click(move |_, _, cx| cx.open_with_system(&path)),
                );
            }
            if state.log_capped {
                tools = tools.child(label("Replay limit reached", theme).text_color(theme.warning));
            }
        }
        panel = panel.child(tools);
        if lines.is_empty() {
            return panel.child(
                label(
                    if problems {
                        "No matching compiler diagnostics."
                    } else {
                        "No matching output yet."
                    },
                    theme,
                )
                .p_2(),
            );
        }
        let count = lines.len();
        panel.child(
            uniform_list(
                "build-output",
                count,
                cx.processor(move |this, range: std::ops::Range<usize>, _, _| {
                    let Some(job) = this.job() else {
                        return Vec::new();
                    };
                    range
                        .filter_map(|row| job.output.lines.get(lines[row]))
                        .map(|line| {
                            let diagnostic = problem(line);
                            let text = match diagnostic {
                                Some((file, row, column, level, message)) if problems => {
                                    format!("{level} · {file}:{row}:{column}  {message}")
                                }
                                _ => line.to_owned(),
                            };
                            let color = match diagnostic {
                                Some((_, _, _, "Error", _)) => theme.error,
                                Some((_, _, _, "Warning", _)) => theme.warning,
                                _ => theme.text,
                            };
                            div()
                                .px_2()
                                .h(px(20.))
                                .text_xs()
                                .font_family("monospace")
                                .text_color(color)
                                .text_ellipsis()
                                .child(text)
                        })
                        .collect()
                }),
            )
            .track_scroll(self.log_scroll.clone())
            .w_full()
            .flex_1()
            .min_h_0(),
        )
    }

    fn history_view(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        div().flex_1().min_h_0().child(
            div()
                .id("history-list")
                .h_full()
                .overflow_y_scroll()
                .when(self.history.is_empty(), |list| {
                    list.child(label("No builds in this session.", theme).p_2())
                })
                .children(self.history.iter().map(|build| {
                    let item = build.clone();
                    let id = item.build_id.clone();
                    let compare = id.clone();
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .px_2()
                        .py_1()
                        .border_b_1()
                        .border_color(theme.border)
                        .child(div().flex_1().child(format!(
                            "{}  ·  {}  ·  {}  ·  {}",
                            id,
                            item.operation,
                            if item.options.profile.is_empty() {
                                "default"
                            } else {
                                &item.options.profile
                            },
                            format!(
                                "{} · {} · {} ms",
                                item.status,
                                selector(&item.compiler),
                                item.duration_ms
                            )
                        )))
                        .child(label(item.started_at.clone().unwrap_or_default(), theme))
                        .child(
                            control(
                                SharedString::from(format!("compare-{id}")),
                                "Compare",
                                theme,
                                false,
                            )
                            .on_click(cx.listener(
                                move |this, _, _, cx| {
                                    this.show_analysis(compare.clone());
                                    this.drawer = Drawer::Analysis;
                                    cx.notify();
                                },
                            )),
                        )
                        .child(
                            control(
                                SharedString::from(format!("inspect-{id}")),
                                "Inspect",
                                theme,
                                false,
                            )
                            .on_click(cx.listener(
                                move |this, _, _, cx| {
                                    if let Some(ws) = this.active.clone() {
                                        this.selected_builds.insert(ws, id.clone());
                                        this.jobs.entry(id.clone()).or_default().state =
                                            Some(item.clone());
                                        this.show_analysis(id.clone());
                                        this.drawer = Drawer::Analysis;
                                    }
                                    cx.notify();
                                },
                            )),
                        )
                })),
        )
    }

    fn analysis_view(&self, theme: Theme, cx: &mut Context<Self>) -> Div {
        let base = div().flex_1().min_h_0().flex().flex_col();
        let Some(analysis) = &self.analysis else {
            return base.child(label("Run a build or select a build in History.", theme).p_2());
        };
        let mut indices: Vec<_> = (0..analysis.timings.len()).collect();
        indices.sort_by_key(|&index| {
            std::cmp::Reverse(analysis.timings[index].end_ms - analysis.timings[index].start_ms)
        });
        let span = analysis
            .timings
            .iter()
            .map(|step| step.end_ms)
            .max()
            .unwrap_or(0);
        let timing_summary = format!(
            "{} measured outputs · recorded span {} ms",
            analysis.timings.len(),
            span
        );
        let base = base.child(
            div()
                .flex()
                .gap_2()
                .px_2()
                .py_1()
                .items_center()
                .child(label(timing_summary, theme))
                .child(
                    control(
                        "ninja-details",
                        if self.show_ninja_details {
                            "Timeline"
                        } else {
                            "Ninja details"
                        },
                        theme,
                        self.show_ninja_details,
                    )
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.show_ninja_details = !this.show_ninja_details;
                        cx.notify();
                    })),
                )
                .when_some(self.comparison.as_ref(), |base, comparison| {
                    base.child(label(
                        format!(
                            "Compare · {} outputs · wall time {:+} ms",
                            comparison.timings.len(),
                            comparison.build.duration_ms as i64 - analysis.build.duration_ms as i64
                        ),
                        theme,
                    ))
                }),
        );
        if self.show_ninja_details {
            let mut details = div()
                .id("ninja-detail-list")
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .px_3()
                .py_2()
                .child(label("Compiler cache statistics unavailable", theme));
            if analysis.ninja_invocations.is_empty() {
                details = details.child(label("No Ninja invocation data was recorded.", theme));
            }
            for invocation in &analysis.ninja_invocations {
                let name = if invocation.member.is_empty() {
                    "Project"
                } else {
                    &invocation.member
                };
                details = details.child(
                    title(if invocation.no_work {
                        format!("{name} · up to date")
                    } else {
                        format!("{name} · {}", invocation.status)
                    })
                    .mt_2(),
                );
                if let Some(path) = &invocation.critical_path {
                    details = details
                        .child(label(
                            format!("Measured plan critical path · {} ms", path.duration_ms),
                            theme,
                        ))
                        .children(path.actions.iter().map(|step| {
                            label(
                                format!(
                                    "{} ms · {} · {}",
                                    step.duration_ms, step.action, step.output
                                ),
                                theme,
                            )
                        }));
                }
                if !invocation.reasons.is_empty() {
                    details = details.child(title("Rebuild reasons").mt_2()).children(
                        invocation
                            .reasons
                            .iter()
                            .map(|reason| label(reason.clone(), theme)),
                    );
                }
                if invocation.reasons_truncated {
                    details = details.child(label(
                        "More reasons are available in the full build log.",
                        theme,
                    ));
                }
            }
            return base.child(details);
        }
        if indices.is_empty() {
            return base.child(
                label(
                    "Ninja appended no attributable timing records for this run.",
                    theme,
                )
                .p_2(),
            );
        }
        base.child(
            uniform_list(
                "timings",
                indices.len(),
                cx.processor(move |this, range: std::ops::Range<usize>, _, _| {
                    let Some(analysis) = &this.analysis else {
                        return Vec::new();
                    };
                    range
                        .filter_map(|row| analysis.timings.get(indices[row]))
                        .map(|timing| {
                            let start = timing.start_ms as f32 / span.max(1) as f32;
                            let width =
                                (timing.end_ms - timing.start_ms) as f32 / span.max(1) as f32;
                            div()
                                .w_full()
                                .h(px(27.))
                                .px_2()
                                .flex()
                                .items_center()
                                .gap_2()
                                .border_b_1()
                                .border_color(theme.border)
                                .child(
                                    div()
                                        .w(relative(0.47))
                                        .text_xs()
                                        .text_ellipsis()
                                        .child(format!("{}  {}", timing.member, timing.output)),
                                )
                                .child(
                                    div()
                                        .flex_1()
                                        .h(px(8.))
                                        .relative()
                                        .rounded_md()
                                        .bg(theme.surface)
                                        .child(
                                            div()
                                                .absolute()
                                                .left(relative(start))
                                                .w(relative(width))
                                                .min_w(px(1.))
                                                .h_full()
                                                .rounded_md()
                                                .bg(theme.accent),
                                        ),
                                )
                                .child(
                                    label(format!("{} ms", timing.end_ms - timing.start_ms), theme)
                                        .w(px(68.)),
                                )
                        })
                        .collect()
                }),
            )
            .flex_1()
            .min_h_0(),
        )
    }
}

fn selector(value: &str) -> &str {
    if value.is_empty() { "auto" } else { value }
}
