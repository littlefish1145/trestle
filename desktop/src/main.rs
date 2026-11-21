#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod graph_view;
mod window;

use gpui::{
    App, AppContext, Application, Bounds, KeyBinding, TitlebarOptions, WindowBounds, WindowOptions,
    px, size,
};
use window::*;

fn main() {
    let initial = std::env::args_os().nth(1).map(std::path::PathBuf::from);
    Application::new().run(move |cx: &mut App| {
        cx.bind_keys([
            KeyBinding::new("secondary-o", OpenWorkspace, None),
            KeyBinding::new("secondary-b", BuildSelected, None),
            KeyBinding::new("secondary-k", Commands, None),
            KeyBinding::new("secondary-f", Search, None),
            KeyBinding::new("secondary-w", CloseWindow, None),
        ]);
        cx.on_window_closed(|cx| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        let bounds = Bounds::centered(None, size(px(1310.), px(850.)), cx);
        let result = cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(680.), px(500.))),
                titlebar: Some(TitlebarOptions {
                    title: Some("Trestle Desktop".into()),
                    ..Default::default()
                }),
                ..Default::default()
            },
            |window, cx| cx.new(|cx| Desktop::new(initial, window, cx)),
        );
        match result {
            Ok(_) => cx.activate(true),
            Err(error) => {
                eprintln!("Cannot open Trestle Desktop: {error}");
                cx.quit();
            }
        }
    });
}
