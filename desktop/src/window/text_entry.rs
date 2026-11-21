use super::{Desktop, Input, view::Theme};
use gpui::{prelude::*, *};
use std::ops::Range;

#[derive(Default)]
pub(super) struct Selection {
    range: Range<usize>,
    marked: Option<Range<usize>>,
    line: Option<(ShapedLine, Bounds<Pixels>)>,
}

fn from_utf16(text: &str, offset: usize) -> usize {
    let mut units = 0;
    for (index, ch) in text.char_indices() {
        if units + ch.len_utf16() > offset {
            return index;
        }
        units += ch.len_utf16();
    }
    text.len()
}

fn range_from_utf16(text: &str, range: Range<usize>) -> Range<usize> {
    let start = from_utf16(text, range.start);
    start..from_utf16(text, range.end).max(start)
}

fn to_utf16(text: &str, range: Range<usize>) -> Range<usize> {
    text[..range.start].encode_utf16().count()..text[..range.end].encode_utf16().count()
}

impl Desktop {
    pub(super) fn show_input(&mut self, kind: Input) {
        self.input = kind;
        let end = self.entry_text().len();
        self.entry_selection = Selection {
            range: end..end,
            ..Default::default()
        };
        self.focus_input = true;
    }

    fn entry_text(&self) -> &str {
        match self.input {
            Input::Path => &self.path_input,
            Input::Find => &self.query,
            Input::None => "",
        }
    }

    fn valid_range(&self, selection: Range<usize>) -> Range<usize> {
        let text = self.entry_text();
        let start = text.floor_char_boundary(selection.start.min(text.len()));
        start
            ..text
                .floor_char_boundary(selection.end.min(text.len()))
                .max(start)
    }

    fn entry_range(&self) -> Range<usize> {
        self.valid_range(self.entry_selection.range.clone())
    }

    fn replace_entry(
        &mut self,
        range_utf16: Option<Range<usize>>,
        replacement: &str,
    ) -> Option<Range<usize>> {
        let original = self.entry_text();
        let range = range_utf16
            .map(|range| range_from_utf16(original, range))
            .or_else(|| self.entry_selection.marked.clone())
            .unwrap_or_else(|| self.entry_range());
        let range = self.valid_range(range);
        let text = match self.input {
            Input::Path => &mut self.path_input,
            Input::Find => &mut self.query,
            Input::None => return None,
        };
        let room = 4096usize.saturating_sub(text.len() - range.len());
        let mut inserted: String = replacement[..replacement.floor_char_boundary(room)]
            .chars()
            .filter(|ch| !ch.is_control())
            .collect();
        inserted.truncate(inserted.floor_char_boundary(room));
        text.replace_range(range.clone(), &inserted);
        let end = range.start + inserted.len();
        self.entry_selection.range = end..end;
        self.entry_selection.marked = None;
        Some(range.start..end)
    }

    pub(super) fn edit_input_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let key = &event.keystroke;
        let needle = key.key.as_str();
        let range = self.entry_range();
        let previous = self.entry_text()[..range.start]
            .char_indices()
            .next_back()
            .map_or(0, |(i, _)| i);
        let next = self.entry_text()[range.end..]
            .chars()
            .next()
            .map_or(range.end, |ch| range.end + ch.len_utf8());
        match needle {
            "backspace" | "delete" => {
                let range = if !range.is_empty() {
                    range
                } else if needle == "backspace" {
                    previous..range.start
                } else {
                    range.end..next
                };
                self.entry_selection.range = range;
                self.replace_entry(None, "");
            }
            "left" | "right" | "home" | "end" => {
                let point = match needle {
                    "left" if range.is_empty() => previous,
                    "left" => range.start,
                    "right" if range.is_empty() => next,
                    "right" => range.end,
                    "home" => 0,
                    _ => self.entry_text().len(),
                };
                self.entry_selection.range = point..point;
                self.entry_selection.marked = None;
            }
            "a" if key.modifiers.secondary() => {
                self.entry_selection.range = 0..self.entry_text().len();
                self.entry_selection.marked = None;
            }
            "c" | "x" if key.modifiers.secondary() => {
                cx.write_to_clipboard(ClipboardItem::new_string(
                    self.entry_text()[range.clone()].to_owned(),
                ));
                if needle == "x" {
                    self.replace_entry(None, "");
                }
            }
            "v" if key.modifiers.secondary() => {
                if let Some(text) = cx.read_from_clipboard().and_then(|item| item.text()) {
                    self.replace_entry(None, &text);
                }
            }
            _ => return,
        }
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn entry_surface(&self, theme: Theme, cx: &mut Context<Self>) -> Stateful<Div> {
        let entity = cx.entity();
        let focus = self.input_focus.clone();
        let text: SharedString = self.entry_text().to_owned().into();
        let selected = self.entry_range();
        let marked = self
            .entry_selection
            .marked
            .clone()
            .map(|range| self.valid_range(range));
        div()
            .id("workspace-entry")
            .track_focus(&self.input_focus)
            .tab_index(0)
            .flex_1()
            .min_w_0()
            .rounded_md()
            .border_1()
            .border_color(theme.border)
            .bg(theme.surface)
            .px_2()
            .h(px(25.))
            .focus(move |style| style.border_color(theme.accent))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, event: &MouseDownEvent, window, cx| {
                    if let Some((line, bounds)) = &this.entry_selection.line {
                        let index = line.closest_index_for_x(event.position.x - bounds.left());
                        let byte = index.min(this.entry_text().len());
                        this.entry_selection.range = byte..byte;
                        this.entry_selection.marked = None;
                    }
                    this.input_focus.focus(window);
                    cx.notify();
                }),
            )
            .child(
                canvas(
                    |_, _, _| (),
                    move |bounds, (), window, cx| {
                        window.handle_input(
                            &focus,
                            ElementInputHandler::new(bounds, entity.clone()),
                            cx,
                        );
                        let style = window.text_style();
                        let runs = [TextRun {
                            len: text.len(),
                            font: style.font(),
                            color: theme.text.into(),
                            background_color: None,
                            underline: None,
                            strikethrough: None,
                        }];
                        let line = window.text_system().shape_line(text, px(13.), &runs, None);
                        let offset = (line.x_for_index(selected.end) + px(2.) - bounds.size.width)
                            .max(px(0.));
                        let paint_bounds =
                            Bounds::new(bounds.origin - point(offset, px(0.)), bounds.size);
                        let range_rect = |range: Range<usize>, top: Pixels, height: Pixels| {
                            let left = line.x_for_index(range.start);
                            let right = line.x_for_index(range.end);
                            Bounds::new(
                                point(paint_bounds.left() + left.min(right), top),
                                size((right - left).abs().max(px(1.)), height),
                            )
                        };
                        window.with_content_mask(Some(ContentMask { bounds }), |window| {
                            if focus.is_focused(window) && !selected.is_empty() {
                                window.paint_quad(fill(
                                    range_rect(selected.clone(), bounds.top() + px(3.), px(18.)),
                                    theme.accent_bg,
                                ));
                            }
                            let _ = line.paint(
                                paint_bounds.origin + point(px(0.), px(3.)),
                                px(19.),
                                window,
                                cx,
                            );
                            if let Some(range) = marked {
                                window.paint_quad(fill(
                                    range_rect(range, bounds.bottom() - px(3.), px(1.)),
                                    theme.accent,
                                ));
                            }
                            if focus.is_focused(window) && selected.is_empty() {
                                window.paint_quad(fill(
                                    range_rect(selected, bounds.top() + px(3.), px(17.)),
                                    theme.accent,
                                ));
                            }
                        });
                        entity.update(cx, |this, _| {
                            this.entry_selection.line = Some((line, paint_bounds))
                        });
                    },
                )
                .size_full(),
            )
    }
}

impl EntityInputHandler for Desktop {
    fn text_for_range(
        &mut self,
        range: Range<usize>,
        adjusted: &mut Option<Range<usize>>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<String> {
        let text = self.entry_text();
        let range = range_from_utf16(text, range);
        *adjusted = Some(to_utf16(text, range.clone()));
        Some(text[range].to_owned())
    }

    fn selected_text_range(
        &mut self,
        _: bool,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<UTF16Selection> {
        (self.input != Input::None).then(|| UTF16Selection {
            range: to_utf16(self.entry_text(), self.entry_range()),
            reversed: false,
        })
    }

    fn marked_text_range(&self, _: &mut Window, _: &mut Context<Self>) -> Option<Range<usize>> {
        self.entry_selection
            .marked
            .clone()
            .map(|range| to_utf16(self.entry_text(), self.valid_range(range)))
    }

    fn unmark_text(&mut self, _: &mut Window, cx: &mut Context<Self>) {
        self.entry_selection.marked = None;
        cx.notify();
    }

    fn replace_text_in_range(
        &mut self,
        range: Option<Range<usize>>,
        text: &str,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.replace_entry(range, text);
        cx.notify();
    }

    fn replace_and_mark_text_in_range(
        &mut self,
        range: Option<Range<usize>>,
        text: &str,
        selected: Option<Range<usize>>,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(inserted) = self.replace_entry(range, text) {
            if let Some(selected) = selected {
                let relative = range_from_utf16(&self.entry_text()[inserted.clone()], selected);
                self.entry_selection.range =
                    (inserted.start + relative.start)..(inserted.start + relative.end);
            }
            self.entry_selection.marked = (!inserted.is_empty()).then_some(inserted);
        }
        cx.notify();
    }

    fn bounds_for_range(
        &mut self,
        range: Range<usize>,
        _: Bounds<Pixels>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<Bounds<Pixels>> {
        let (line, bounds) = self.entry_selection.line.as_ref()?;
        let range = range_from_utf16(self.entry_text(), range);
        let start = bounds.left() + line.x_for_index(range.start);
        let end = bounds.left() + line.x_for_index(range.end);
        Some(Bounds::new(
            point(start.min(end), bounds.top()),
            size((end - start).abs().max(px(1.)), bounds.size.height),
        ))
    }

    fn character_index_for_point(
        &mut self,
        point: Point<Pixels>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<usize> {
        let (line, bounds) = self.entry_selection.line.as_ref()?;
        let byte = line.closest_index_for_x(point.x - bounds.left());
        Some(
            self.entry_text()[..self.entry_text().floor_char_boundary(byte)]
                .encode_utf16()
                .count(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::{from_utf16, range_from_utf16, to_utf16};

    #[test]
    fn maps_utf16_composition_without_splitting_unicode() {
        let text = "路🙂径";
        assert_eq!(range_from_utf16(text, 1..3), 3..7);
        assert_eq!(to_utf16(text, 3..7), 1..3);
        assert_eq!(from_utf16(text, 2), 3);
        assert_eq!(from_utf16(text, 20), text.len());
    }
}
