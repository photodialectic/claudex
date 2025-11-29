from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence

from markdown_it import MarkdownIt
from markdown_it.token import Token

md = MarkdownIt("commonmark")


@dataclass
class Span:
    start: int
    end: int
    style: dict


class DocsRequestBuilder:
    def __init__(
        self,
        start_index: int = 1,
        prepend_newline: bool = False,
        tab_id: str | None = None,
    ):
        self.cursor = start_index
        self.requests: list[dict] = []
        self.tab_id = tab_id
        if prepend_newline:
            self._insert_text("\n")

    def _insert_text(self, text: str) -> int:
        if not text:
            return self.cursor
        location = {"index": self.cursor}
        if self.tab_id:
            location["tabId"] = self.tab_id
        self.requests.append(
            {"insertText": {"location": location, "text": text}}
        )
        start = self.cursor
        self.cursor += len(text)
        return start

    def add_paragraph(
        self,
        text: str,
        paragraph_style: dict | None = None,
        spans: Sequence[Span] | None = None,
        list_type: str | None = None,
    ) -> None:
        if not text:
            return
        if not text.endswith("\n"):
            text_to_insert = text + "\n"
        else:
            text_to_insert = text
        text_length = len(text_to_insert)
        start = self._insert_text(text_to_insert)
        end = start + text_length

        if paragraph_style:
            fields = ",".join(paragraph_style.keys())
            update_range = {"startIndex": start, "endIndex": end}
            if self.tab_id:
                update_range["tabId"] = self.tab_id
            self.requests.append(
                {
                    "updateParagraphStyle": {
                        "range": update_range,
                        "paragraphStyle": paragraph_style,
                        "fields": fields,
                    }
                }
            )

        if spans:
            for span in spans:
                if span.end <= span.start:
                    continue
                fields = ",".join(span.style.keys())
                update_range = {
                    "startIndex": start + span.start,
                    "endIndex": start + span.end,
                }
                if self.tab_id:
                    update_range["tabId"] = self.tab_id
                self.requests.append(
                    {
                        "updateTextStyle": {
                            "range": update_range,
                            "textStyle": span.style,
                            "fields": fields,
                        }
                    }
                )

        if list_type:
            preset = (
                "NUMBERED_DECIMAL_ALPHA_ROMAN"
                if list_type == "ordered"
                else "BULLET_DISC_CIRCLE_SQUARE"
            )
            bullet_range = {"startIndex": start, "endIndex": end}
            if self.tab_id:
                bullet_range["tabId"] = self.tab_id
            self.requests.append(
                {
                    "createParagraphBullets": {
                        "range": bullet_range,
                        "bulletPreset": preset,
                    }
                }
            )


def _parse_inline(inline_token: Token) -> tuple[str, list[Span]]:
    text_parts: list[str] = []
    spans: list[Span] = []
    cursor = 0
    stack: list[dict] = []

    def push(kind: str, meta: dict | None = None):
        stack.append({"kind": kind, "start": cursor, "meta": meta or {}})

    def pop(kind: str):
        for idx in range(len(stack) - 1, -1, -1):
            if stack[idx]["kind"] == kind:
                return stack.pop(idx)
        return None

    children = inline_token.children or []
    for child in children:
        if child.type == "text":
            text_parts.append(child.content)
            cursor += len(child.content)
        elif child.type in {"softbreak", "hardbreak"}:
            text_parts.append("\n")
            cursor += 1
        elif child.type == "strong_open":
            push("bold")
        elif child.type == "strong_close":
            entry = pop("bold")
            if entry and entry["start"] < cursor:
                spans.append(Span(entry["start"], cursor, {"bold": True}))
        elif child.type == "em_open":
            push("italic")
        elif child.type == "em_close":
            entry = pop("italic")
            if entry and entry["start"] < cursor:
                spans.append(Span(entry["start"], cursor, {"italic": True}))
        elif child.type == "link_open":
            href = dict(child.attrs or {}).get("href")
            push("link", {"href": href})
        elif child.type == "link_close":
            entry = pop("link")
            if entry and entry["start"] < cursor and entry["meta"].get("href"):
                spans.append(
                    Span(
                        entry["start"],
                        cursor,
                        {"link": {"url": entry["meta"]["href"]}},
                    )
                )
        elif child.type == "code_inline":
            text = child.content
            text_parts.append(text)
            start = cursor
            cursor += len(text)
            spans.append(
                Span(
                    start,
                    cursor,
                    {
                        "weightedFontFamily": {
                            "fontFamily": "Roboto Mono",
                            "weight": 400,
                        }
                    },
                )
            )

    return "".join(text_parts), spans


def _render_list(builder: DocsRequestBuilder, tokens: list[Token], i: int, list_type: str) -> int:
    close_type = "bullet_list_close" if list_type == "bullet" else "ordered_list_close"
    i += 1
    while i < len(tokens):
        token = tokens[i]
        if token.type == close_type:
            return i + 1
        if token.type == "list_item_open":
            i += 1
            while i < len(tokens) and tokens[i].type != "list_item_close":
                if tokens[i].type == "paragraph_open":
                    inline = tokens[i + 1]
                    text, spans = _parse_inline(inline)
                    builder.add_paragraph(text, spans=spans, list_type=list_type)
                    i += 3
                else:
                    i += 1
            i += 1  # skip list_item_close
        else:
            i += 1
    return i


def build_markdown_requests(
    markdown_text: str,
    start_index: int = 1,
    *,
    prepend_newline: bool = False,
    tab_id: str | None = None,
) -> tuple[list[dict], int]:
    if not markdown_text:
        return [], start_index
    tokens = md.parse(markdown_text)
    builder = DocsRequestBuilder(
        start_index=start_index,
        prepend_newline=prepend_newline,
        tab_id=tab_id,
    )
    i = 0
    while i < len(tokens):
        token = tokens[i]
        if token.type == "heading_open":
            level_str = token.tag[1:] if token.tag.startswith("h") else token.tag
            level = int(level_str) if level_str.isdigit() else 1
            inline = tokens[i + 1]
            text, spans = _parse_inline(inline)
            builder.add_paragraph(
                text,
                spans=spans,
                paragraph_style={"namedStyleType": f"HEADING_{min(level, 6)}"},
            )
            i += 3
        elif token.type == "paragraph_open":
            inline = tokens[i + 1]
            text, spans = _parse_inline(inline)
            if text.strip():
                builder.add_paragraph(text, spans=spans)
            i += 3
        elif token.type == "bullet_list_open":
            i = _render_list(builder, tokens, i, list_type="bullet")
        elif token.type == "ordered_list_open":
            i = _render_list(builder, tokens, i, list_type="ordered")
        elif token.type == "fence":
            code_text = token.content.rstrip("\n")
            if code_text:
                # Simpler approach: just use monospace font + gray background on the paragraph
                # Create span for monospace font styling and background
                code_span = Span(
                    0,
                    len(code_text),
                    {
                        "weightedFontFamily": {
                            "fontFamily": "Roboto Mono",
                            "weight": 400,
                        },
                        "fontSize": {"magnitude": 9, "unit": "PT"},
                        "backgroundColor": {
                            "color": {
                                "rgbColor": {
                                    "red": 0.95,
                                    "green": 0.95,
                                    "blue": 0.95,
                                }
                            }
                        },
                    },
                )
                # Add indentation to make it look like a block
                paragraph_style = {
                    "indentStart": {"magnitude": 18, "unit": "PT"},
                    "indentEnd": {"magnitude": 18, "unit": "PT"},
                    "spaceAbove": {"magnitude": 6, "unit": "PT"},
                    "spaceBelow": {"magnitude": 6, "unit": "PT"},
                }
                builder.add_paragraph(
                    code_text, paragraph_style=paragraph_style, spans=[code_span]
                )
            i += 1
        else:
            i += 1
    return builder.requests, builder.cursor
