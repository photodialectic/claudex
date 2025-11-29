from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable, Iterator, Optional

from googleapiclient.discovery import build
from googleapiclient.errors import HttpError

from .auth import GoogleAuthManager, auth_manager
from .exceptions import GoogleDocsError, MissingGoogleCredentials
from .markdown_renderer import build_markdown_requests


def _doc_url(document_id: str) -> str:
    return f"https://docs.google.com/document/d/{document_id}/edit"


def _is_code_block_paragraph(paragraph: dict) -> bool:
    """Check if a paragraph is a code block."""
    # Check for native Google Docs code blocks (U+E907 marker)
    elements = paragraph.get("elements", [])
    if elements:
        first_element = elements[0]
        if "textRun" in first_element:
            content = first_element["textRun"].get("content", "")
            # Check for the code block marker character
            if content and content[0] == '\ue907':
                return True

            # Check for our custom code blocks (monospace + gray background)
            text_style = first_element["textRun"].get("textStyle", {})
            font_family = text_style.get("weightedFontFamily", {}).get("fontFamily", "")
            bg_color = text_style.get("backgroundColor", {}).get("color", {}).get("rgbColor", {})

            has_monospace = "Mono" in font_family or "Courier" in font_family
            has_gray_bg = (
                bg_color.get("red", 0) > 0.9 and
                bg_color.get("green", 0) > 0.9 and
                bg_color.get("blue", 0) > 0.9
            )

            if has_monospace and has_gray_bg:
                return True

    return False


def _extract_text_from_table(table: dict) -> str:
    """Extract text content from a table."""
    text_parts = []
    for row in table.get("tableRows", []):
        for cell in row.get("tableCells", []):
            for content_element in cell.get("content", []):
                if "paragraph" in content_element:
                    para = content_element["paragraph"]
                    for element in para.get("elements", []):
                        if "textRun" in element:
                            text = element["textRun"].get("content", "")
                            if text:
                                text_parts.append(text)
    return "".join(text_parts)


def _is_code_block_table(table: dict) -> bool:
    """Check if a 1x1 table with monospace font is a code block."""
    rows = table.get("tableRows", [])
    # Must be a single row
    if len(rows) != 1:
        return False

    cells = rows[0].get("tableCells", [])
    # Must be a single column
    if len(cells) != 1:
        return False

    # Check if the cell has gray background
    cell_style = cells[0].get("tableCellStyle", {})
    bg_color = cell_style.get("backgroundColor", {}).get("color", {}).get("rgbColor", {})
    has_gray_bg = (
        bg_color.get("red", 0) > 0.9 and
        bg_color.get("green", 0) > 0.9 and
        bg_color.get("blue", 0) > 0.9
    )

    # Check if content uses monospace font
    has_monospace = False
    for content_element in cells[0].get("content", []):
        if "paragraph" in content_element:
            para = content_element["paragraph"]
            for element in para.get("elements", []):
                if "textRun" in element:
                    text_style = element["textRun"].get("textStyle", {})
                    font_family = text_style.get("weightedFontFamily", {}).get("fontFamily", "")
                    if "Mono" in font_family or "Courier" in font_family:
                        has_monospace = True
                        break

    return has_gray_bg and has_monospace


def _extract_plain_text(document: dict) -> str:
    """Flatten Doc body content into plaintext, preserving code block markers."""
    chunks: list[str] = []
    in_code_block = False

    for body_element in document.get("body", {}).get("content", []):
        # Handle tables (potential code blocks)
        if "table" in body_element:
            table = body_element["table"]
            is_code_table = _is_code_block_table(table)

            if is_code_table:
                chunks.append("```\n")
                chunks.append(_extract_text_from_table(table))
                chunks.append("```\n")
            else:
                # Regular table - just extract text without code markers
                chunks.append(_extract_text_from_table(table))
            continue

        # Handle paragraphs
        paragraph = body_element.get("paragraph")
        if not paragraph:
            continue

        # Check paragraph style for headings
        para_style = paragraph.get("paragraphStyle", {})
        named_style = para_style.get("namedStyleType", "")

        # Check if this paragraph uses the CODE named style (native code blocks)
        is_code_paragraph = _is_code_block_paragraph(paragraph)

        # Extract text content
        paragraph_text_parts = []
        for i, element in enumerate(paragraph.get("elements", [])):
            text_run = element.get("textRun")
            if text_run:
                text = text_run.get("content", "")
                if text:
                    # Skip the code block marker character (U+E907) at the start
                    if i == 0 and is_code_paragraph and text[0] == '\ue907':
                        text = text[1:]  # Remove the marker
                    if text:
                        paragraph_text_parts.append(text)

        paragraph_text = "".join(paragraph_text_parts).rstrip("\n")

        # Handle transitions between code and non-code
        if is_code_paragraph and not in_code_block:
            chunks.append("```\n")
            in_code_block = True
        elif not is_code_paragraph and in_code_block:
            chunks.append("```\n")
            in_code_block = False

        if paragraph_text:
            # Add heading markers based on style
            if named_style.startswith("HEADING_"):
                level = named_style.replace("HEADING_", "")
                if level.isdigit():
                    heading_marker = "#" * int(level)
                    chunks.append(f"{heading_marker} {paragraph_text}\n\n")
                else:
                    chunks.append(f"{paragraph_text}\n")
            else:
                chunks.append(f"{paragraph_text}\n")

    # Close any open code block
    if in_code_block:
        chunks.append("```\n")

    return "".join(chunks).strip()


@dataclass(kw_only=True)
class DocumentSummary:
    document_id: str
    url: str
    title: str | None = None
    tab_id: str | None = None

    def as_dict(self) -> dict:
        return {
            "document_id": self.document_id,
            "title": self.title,
            "url": self.url,
            "tab_id": self.tab_id,
        }


@dataclass(kw_only=True)
class DocumentContent(DocumentSummary):
    content: str

    def as_dict(self) -> dict:
        base = super().as_dict()
        base["content"] = self.content
        return base


class GoogleDocsService:
    """
    Thin wrapper around the Google Docs API with error handling.
    """

    def __init__(self, auth: GoogleAuthManager | None = None):
        self.auth = auth or auth_manager

    def _client(self):
        creds = self.auth.require_credentials()
        try:
            return build("docs", "v1", credentials=creds, cache_discovery=False)
        except Exception as exc:  # pragma: no cover - safety net
            raise GoogleDocsError(f"Failed to initialize Docs client: {exc}") from exc

    def _drive_client(self):
        creds = self.auth.require_credentials()
        try:
            return build("drive", "v3", credentials=creds, cache_discovery=False)
        except Exception as exc:  # pragma: no cover - safety net
            raise GoogleDocsError(f"Failed to initialize Drive client: {exc}") from exc

    def _get_document(
        self,
        document_id: str,
        *,
        include_tabs: bool = False,
        service=None,
    ) -> dict:
        service = service or self._client()
        kwargs = {"documentId": document_id}
        if include_tabs:
            kwargs["includeTabsContent"] = True
        return service.documents().get(**kwargs).execute()

    def _iter_tabs(
        self, tabs: Iterable[dict], depth: int = 0, parent_id: str | None = None
    ) -> Iterator[tuple[dict, int, str | None]]:
        for tab in tabs or []:
            yield tab, depth, parent_id
            yield from self._iter_tabs(
                tab.get("childTabs", []),
                depth + 1,
                tab.get("tabProperties", {}).get("tabId"),
            )

    def _flatten_tabs(self, doc: dict) -> list[dict]:
        return [tab for tab, _, _ in self._iter_tabs(doc.get("tabs", []))]

    def _list_tab_metadata(self, doc: dict) -> list["TabMetadata"]:
        metadata: list[TabMetadata] = []
        for tab, depth, parent_id in self._iter_tabs(doc.get("tabs", [])):
            props = tab.get("tabProperties", {})
            metadata.append(
                TabMetadata(
                    tab_id=props.get("tabId"),
                    title=props.get("title"),
                    depth=depth,
                    parent_id=parent_id,
                )
            )
        if not metadata:
            metadata.append(
                TabMetadata(
                    tab_id=None,
                    title=doc.get("title"),
                    depth=0,
                    parent_id=None,
                )
            )
        return metadata

    def _resolve_tab(
        self, doc: dict, tab_id: str | None
    ) -> tuple[Optional[dict], Optional[str]]:
        if tab_id:
            for tab in self._flatten_tabs(doc):
                props = tab.get("tabProperties", {})
                if props.get("tabId") == tab_id:
                    return tab, tab_id
            raise GoogleDocsError(f"Tab '{tab_id}' not found in document.")
        tabs = self._flatten_tabs(doc)
        if tabs:
            props = tabs[0].get("tabProperties", {})
            return tabs[0], props.get("tabId")
        return None, None

    def _tab_body(self, doc: dict, tab: Optional[dict]) -> dict:
        if tab:
            return tab.get("documentTab", {})
        return doc

    def _body_end_index(self, body_container: dict | None) -> int:
        body = (body_container or {}).get("body", {})
        content = body.get("content", [])
        if not content:
            return 1
        return content[-1].get("endIndex", 1)

    def _create_delete_range(self, tab_id: str | None, end_index: int) -> dict:
        delete_range = {
            "startIndex": 1,
            "endIndex": max(end_index - 1, 1),
        }
        if tab_id:
            delete_range["tabId"] = tab_id
        return delete_range

    def create_document(
        self, title: str, initial_text: str | None = None
    ) -> DocumentSummary:
        try:
            if initial_text:
                # Use Drive API to create doc from markdown
                drive_service = self._drive_client()

                file_metadata = {
                    'name': title,
                    'mimeType': 'application/vnd.google-apps.document'
                }

                # Create file with markdown content
                from googleapiclient.http import MediaInMemoryUpload
                media = MediaInMemoryUpload(
                    initial_text.encode('utf-8'),
                    mimetype='text/markdown',
                    resumable=True
                )

                doc = drive_service.files().create(
                    body=file_metadata,
                    media_body=media,
                    fields='id,name'
                ).execute()

                doc_id = doc['id']
            else:
                # No content, use Docs API to create empty doc
                service = self._client()
                doc = service.documents().create(body={"title": title}).execute()
                doc_id = doc["documentId"]

            # Get document to retrieve tab info
            service = self._client()
            full_doc = service.documents().get(documentId=doc_id).execute()
            tabs = full_doc.get("tabs") or []
            default_tab_id = None
            if tabs:
                default_tab_id = tabs[0].get("tabProperties", {}).get("tabId")

            return DocumentSummary(
                document_id=doc_id,
                title=title,
                url=_doc_url(doc_id),
                tab_id=default_tab_id,
            )
        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def append_text(
        self, document_id: str, text: str, tab_id: str | None = None
    ) -> DocumentSummary:
        try:
            # Use read + append + replace approach with Drive API
            # 1. Read current content as markdown
            current_doc = self.fetch_document(document_id, tab_id)
            current_content = current_doc.content

            # 2. Append new text (add newlines if needed)
            if current_content and not current_content.endswith('\n\n'):
                if current_content.endswith('\n'):
                    combined_content = current_content + '\n' + text
                else:
                    combined_content = current_content + '\n\n' + text
            else:
                combined_content = current_content + text

            # 3. Replace document with combined content
            return self.replace_document(document_id, combined_content, tab_id)

        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def replace_document(
        self, document_id: str, text: str, tab_id: str | None = None
    ) -> DocumentSummary:
        try:
            # Use Drive API to update doc with markdown content
            drive_service = self._drive_client()
            docs_service = self._client()

            # Get document for metadata
            doc = self._get_document(document_id, include_tabs=True, service=docs_service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)

            # Update file with markdown content using Drive API
            from googleapiclient.http import MediaInMemoryUpload
            media = MediaInMemoryUpload(
                text.encode('utf-8'),
                mimetype='text/markdown',
                resumable=True
            )

            drive_service.files().update(
                fileId=document_id,
                media_body=media
            ).execute()

            return DocumentSummary(
                document_id=document_id,
                title=doc.get("title"),
                url=_doc_url(document_id),
                tab_id=effective_tab_id,
            )
        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def fetch_document(
        self, document_id: str, tab_id: str | None = None
    ) -> DocumentContent:
        try:
            # Use Drive API to export as markdown
            drive_service = self._drive_client()
            docs_service = self._client()

            # Get document metadata for title
            doc = self._get_document(document_id, include_tabs=True, service=docs_service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)

            # Export document as markdown using Drive API
            # Note: tabs are not supported in markdown export, exports entire doc
            markdown_content = drive_service.files().export(
                fileId=document_id,
                mimeType='text/markdown'
            ).execute()

            # Decode bytes to string
            text = markdown_content.decode('utf-8') if isinstance(markdown_content, bytes) else markdown_content

            return DocumentContent(
                document_id=document_id,
                title=doc.get("title"),
                url=_doc_url(document_id),
                content=text,
                tab_id=effective_tab_id,
            )
        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def list_tabs(self, document_id: str) -> list["TabMetadata"]:
        doc = self._get_document(document_id, include_tabs=True)
        return self._list_tab_metadata(doc)


@dataclass
class TabMetadata:
    tab_id: str | None
    title: str | None
    depth: int
    parent_id: str | None

    def as_dict(self) -> dict:
        return {
            "tab_id": self.tab_id,
            "title": self.title,
            "depth": self.depth,
            "parent_id": self.parent_id,
        }


docs_service = GoogleDocsService()
