use crate::window::{
    Desktop, Direction,
    view::{Theme, control},
};
use gpui::{prelude::*, *};

pub(crate) const SLOT_X: f32 = 208.;
pub(crate) const SLOT_Y: f32 = 64.;
const NODE_W: f32 = 162.;
const NODE_H: f32 = 45.;

pub fn render(
    this: &Desktop,
    theme: Theme,
    viewport: Size<Pixels>,
    cx: &mut Context<Desktop>,
) -> Div {
    let Some(graph) = this.graph.clone() else {
        return div().p_4().text_color(theme.muted).child(
            this.graph_error
                .clone()
                .unwrap_or_else(|| "No graph available.".into()),
        );
    };
    let Some(workspace) = this.workspace() else {
        return div();
    };
    let mut toolbar = div()
        .flex()
        .flex_wrap()
        .items_center()
        .gap_2()
        .p_2()
        .border_b_1()
        .border_color(theme.border)
        .bg(theme.panel);
    for (name, direction) in [
        ("Direct", Direction::Direct),
        ("Transitive", Direction::Transitive),
        ("Reverse", Direction::Reverse),
    ] {
        toolbar = toolbar.child(
            control(name, name, theme, this.direction == direction).on_click(cx.listener(
                move |this, _, _, cx| {
                    this.direction = direction;
                    this.refresh_related();
                    cx.notify();
                },
            )),
        );
    }
    toolbar = toolbar
        .child(
            control(
                "focus-graph",
                "Selected chain",
                theme,
                this.graph_focus_only,
            )
            .on_click(cx.listener(|this, _, _, cx| {
                this.graph_focus_only = !this.graph_focus_only;
                cx.notify();
            })),
        )
        .child(
            control("zoom-out", "−", theme, false).on_click(cx.listener(|this, _, _, cx| {
                this.zoom = (this.zoom / 1.2).max(0.36);
                cx.notify();
            })),
        )
        .child(
            control(
                "reset-graph",
                format!("{:.0}%", this.zoom * 100.),
                theme,
                false,
            )
            .on_click(cx.listener(|this, _, _, cx| {
                this.zoom = 1.;
                this.graph_x = 0.;
                this.graph_y = 0.;
                cx.notify();
            })),
        )
        .child(
            control("zoom-in", "+", theme, false).on_click(cx.listener(|this, _, _, cx| {
                this.zoom = (this.zoom * 1.2).min(2.2);
                cx.notify();
            })),
        )
        .child(
            div()
                .text_xs()
                .text_color(theme.muted)
                .child("Drag to pan · Ctrl-scroll to zoom"),
        );

    if !this.artifact_times.is_empty() {
        toolbar = toolbar.child(
            control(
                "measured-only",
                "Built artifacts",
                theme,
                this.graph_measured_only,
            )
            .on_click(cx.listener(|this, _, _, cx| {
                this.graph_measured_only = !this.graph_measured_only;
                cx.notify();
            })),
        );
    }

    let selected = this.selected_index();
    if let Some(index) = selected.filter(|&index| graph.can_collapse(index)) {
        let collapsed = this.graph_collapsed.contains(&index);
        toolbar = toolbar.child(
            control(
                "collapse-branch",
                if collapsed {
                    "Expand dependencies"
                } else {
                    "Collapse dependencies"
                },
                theme,
                collapsed,
            )
            .on_click(cx.listener(move |this, _, _, cx| {
                this.toggle_branch(index);
                cx.notify();
            })),
        );
    }
    let expanded = this.graph_expanded.clone();
    let expanded_edges = expanded.clone();
    let collapsed_edges = this.graph_collapsed.clone();
    let related = this.graph_related.clone();
    let highlight = related.clone();
    let focus = this.graph_focus_only && selected.is_some();
    let z = this.zoom;
    let measured = this.graph_measured_only.then(|| {
        std::rc::Rc::new(
            workspace
                .targets
                .iter()
                .enumerate()
                .filter_map(|(index, target)| {
                    this.artifact_times
                        .contains_key(&target.id)
                        .then_some(index)
                })
                .collect::<std::collections::HashSet<_>>(),
        )
    });
    let measured_edges = measured.clone();
    let offset_x = this.graph_x;
    let offset_y = this.graph_y;
    let query = this.query.to_lowercase();
    let position = move |node: trestle_desktop::graph::Node| {
        (
            (node.layer as f32 * SLOT_X + 22.) * z + offset_x,
            (node.row as f32 * SLOT_Y + 22.) * z + offset_y,
        )
    };
    let geometry = graph.clone();
    let matches =
        |i: usize| query.is_empty() || workspace.targets[i].id.to_lowercase().contains(&query);
    let visible = |i: usize| {
        if expanded.as_ref().is_some_and(|nodes| !nodes[i]) {
            return false;
        }
        (!focus || selected == Some(i) || related.contains(&i))
            && measured.as_ref().is_none_or(|nodes| nodes.contains(&i))
    };
    let canvas = canvas(
        |_, _, _| (),
        move |bounds, (), window, _| {
            let width = f32::from(bounds.size.width);
            let height = f32::from(bounds.size.height);
            let color = |i, j| {
                if (selected == Some(i) || highlight.contains(&i))
                    && (selected == Some(j) || highlight.contains(&j))
                {
                    theme.accent
                } else {
                    theme.border
                }
            };
            let mut normal = PathBuilder::stroke(px(1.));
            let mut highlighted = PathBuilder::stroke(px(1.5));
            let first = ((-offset_x - 22. * z) / (SLOT_X * z)).floor().max(0.) as usize;
            let last = ((width - offset_x) / (SLOT_X * z)).ceil().max(0.) as usize;
            for (provider, consumer) in geometry.edges_in_layers(first, last) {
                if collapsed_edges.contains(&consumer)
                    || expanded_edges
                        .as_ref()
                        .is_some_and(|nodes| !nodes[provider] || !nodes[consumer])
                {
                    continue;
                }
                if measured_edges
                    .as_ref()
                    .is_some_and(|nodes| !nodes.contains(&provider) || !nodes.contains(&consumer))
                {
                    continue;
                }
                if focus && !(highlight.contains(&provider) || selected == Some(provider)) {
                    continue;
                }
                if focus && !(highlight.contains(&consumer) || selected == Some(consumer)) {
                    continue;
                }
                let (a, b) = position(geometry.nodes[provider]);
                let (c, d) = position(geometry.nodes[consumer]);
                let x1 = a + NODE_W * z;
                let y1 = b + NODE_H * z * 0.5;
                let x2 = c;
                let y2 = d + NODE_H * z * 0.5;
                if x1 > width || x2 < 0. || y1.min(y2) > height || y1.max(y2) < 0. {
                    continue;
                }
                let p = |x, y| bounds.origin + point(px(x), px(y));
                let path = if color(provider, consumer) == theme.accent {
                    &mut highlighted
                } else {
                    &mut normal
                };
                let mid = (x1 + x2) * 0.5;
                path.move_to(p(x1, y1));
                path.line_to(p(mid, y1));
                path.line_to(p(mid, y2));
                path.line_to(p(x2, y2));
                path.move_to(p(x2 - 5. * z, y2 - 3. * z));
                path.line_to(p(x2, y2));
                path.line_to(p(x2 - 5. * z, y2 + 3. * z));
            }
            if let Ok(path) = normal.build() {
                window.paint_path(path, theme.border);
            }
            if let Ok(path) = highlighted.build() {
                window.paint_path(path, theme.accent);
            }
        },
    )
    .absolute()
    .inset_0()
    .size_full();

    // The outer window is an upper bound on the actual graph pane.
    let first_layer = ((-offset_x - 22. * z - NODE_W * z) / (SLOT_X * z))
        .floor()
        .max(0.) as usize;
    let last_layer = ((f32::from(viewport.width) - offset_x) / (SLOT_X * z))
        .ceil()
        .max(0.) as usize;
    let first_row = ((-offset_y - 22. * z - NODE_H * z) / (SLOT_Y * z))
        .floor()
        .max(0.) as usize;
    let last_row = ((f32::from(viewport.height) - offset_y) / (SLOT_Y * z))
        .ceil()
        .max(0.) as usize;
    let mut body = div()
        .id("graph-viewport")
        .relative()
        .flex_1()
        .min_h_0()
        .overflow_hidden()
        .child(canvas)
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(|this, event: &MouseDownEvent, _, _| {
                this.graph_drag = Some(event.position);
            }),
        )
        .on_mouse_move(cx.listener(|this, event: &MouseMoveEvent, _, cx| {
            if let Some(last) = this.graph_drag.replace(event.position) {
                let delta = event.position - last;
                this.graph_x += f32::from(delta.x);
                this.graph_y += f32::from(delta.y);
                cx.notify();
            } else {
                this.graph_drag = None;
            }
        }))
        .on_scroll_wheel(cx.listener(|this, event: &ScrollWheelEvent, _, cx| {
            let (x, y) = match event.delta {
                ScrollDelta::Pixels(delta) => (f32::from(delta.x), f32::from(delta.y)),
                ScrollDelta::Lines(delta) => (30. * delta.x, 30. * delta.y),
            };
            if event.modifiers.control || event.modifiers.platform {
                this.zoom = (this.zoom * (y * 0.006).exp()).clamp(0.36, 2.2);
            } else {
                this.graph_x += x;
                this.graph_y += y;
            }
            cx.notify();
        }));
    for layer in graph.layers.iter().take(last_layer).skip(first_layer) {
        for &index in layer.iter().take(last_row).skip(first_row) {
            if !visible(index) {
                continue;
            }
            let node = graph.nodes[index];
            let target = &workspace.targets[index];
            let (x, y) = position(node);
            let id = target.id.clone();
            let active = selected == Some(index);
            let accent =
                active || related.contains(&index) || (!query.is_empty() && matches(index));
            body = body.child(
                div()
                    .id(("node", index))
                    .absolute()
                    .left(px(x))
                    .top(px(y))
                    .w(px(NODE_W * z))
                    .h(px(NODE_H * z))
                    .rounded_md()
                    .border_1()
                    .border_color(if accent { theme.accent } else { theme.border })
                    .bg(if active { theme.accent_bg } else { theme.panel })
                    .text_color(if matches(index) {
                        theme.text
                    } else {
                        theme.muted
                    })
                    .hover(move |s| s.bg(theme.hover))
                    .px_2()
                    .flex()
                    .flex_col()
                    .justify_center()
                    .cursor_pointer()
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.select_target(id.clone());
                        cx.notify();
                    }))
                    .child(
                        div()
                            .text_size(px(13. * z))
                            .text_ellipsis()
                            .child(target.id.clone()),
                    )
                    .child(
                        div()
                            .text_size(px(10. * z))
                            .text_color(theme.muted)
                            .text_ellipsis()
                            .child(match this.artifact_times.get(&target.id) {
                                Some(ms) => format!("{} · built in {ms} ms", target.kind),
                                None => target.kind.clone(),
                            }),
                    ),
            );
        }
    }
    div()
        .flex()
        .flex_col()
        .flex_1()
        .min_h_0()
        .child(toolbar)
        .child(body)
}
