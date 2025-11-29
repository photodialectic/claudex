from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable, Iterator, Optional

from googleapiclient.discovery import build
from googleapiclient.errors import HttpError
from googleapiclient.http import MediaInMemoryUpload

from .auth import GoogleAuthManager, auth_manager
from .exceptions import GoogleDocsError, MissingGoogleCredentials
from .markdown_renderer import build_markdown_requests


def _doc_url(document_id: str) -> str:
    return f"https://docs.google.com/document/d/{document_id}/edit"


def _extract_plain_text(document: dict) -> str:
    """Flatten Doc body content into a simple plaintext string.

    Note: This is only used as a fallback. The Drive API markdown export
    is preferred as it preserves all formatting.
    """
    chunks: list[str] = []
    for body_element in document.get("body", {}).get("content", []):
        paragraph = body_element.get("paragraph")
        if not paragraph:
            continue
        for element in paragraph.get("elements", []):
            text_run = element.get("textRun")
            if text_run:
                text = text_run.get("content")
                if text:
                    chunks.append(text)
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
        """Create a new Google Doc, optionally with markdown content.

        Note: Uses Drive API markdown conversion for initial_text, which provides
        full formatting support but does not support tabs.
        """
        try:
            if initial_text:
                # Use Drive API to create doc from markdown
                drive_service = self._drive_client()

                file_metadata = {
                    "name": title,
                    "mimeType": "application/vnd.google-apps.document"
                }

                # Create file with markdown content
                media = MediaInMemoryUpload(
                    initial_text.encode("utf-8"),
                    mimetype="text/markdown",
                    resumable=True
                )

                doc = drive_service.files().create(
                    body=file_metadata,
                    media_body=media,
                    fields="id,name"
                ).execute()

                doc_id = doc["id"]
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
        """Append markdown text to a document.

        Note: This uses a read-modify-write approach (fetch entire document,
        append text, replace entire document). For large documents, this is
        less efficient than direct insertion but provides full markdown
        formatting support. Tab operations are not supported.

        Args:
            document_id: The Google Doc ID
            text: Markdown text to append
            tab_id: Must be None (tabs not supported)

        Raises:
            GoogleDocsError: If tab_id is not None
        """
        try:
            # Validate tab_id
            if tab_id is not None:
                raise GoogleDocsError(
                    "Tab-specific append operations are not supported. "
                    "Drive API markdown operations work on entire documents. "
                    "Use tab_id=None to append to the entire document."
                )

            # Use read + append + replace approach with Drive API
            # 1. Read current content as markdown
            current_doc = self.fetch_document(document_id, tab_id)
            current_content = current_doc.content

            # 2. Append new text (add newlines if needed)
            if current_content and not current_content.endswith("\n\n"):
                if current_content.endswith("\n"):
                    combined_content = current_content + "\n" + text
                else:
                    combined_content = current_content + "\n\n" + text
            else:
                combined_content = current_content + text

            # 3. Replace document with combined content
            return self.replace_document(document_id, combined_content, tab_id)

        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def replace_document(
        self, document_id: str, text: str, tab_id: str | None = None
    ) -> DocumentSummary:
        """Replace entire document content with markdown text.

        Note: Drive API markdown import replaces the entire document and does not
        support tab-specific operations. If tab_id is provided, an error is raised.

        Args:
            document_id: The Google Doc ID
            text: Markdown text to replace document with
            tab_id: Must be None (tabs not supported)

        Raises:
            GoogleDocsError: If tab_id is not None
        """
        try:
            # Validate tab_id
            if tab_id is not None:
                raise GoogleDocsError(
                    "Tab-specific replace operations are not supported. "
                    "Drive API markdown import replaces the entire document. "
                    "Use tab_id=None to replace the entire document."
                )

            # Use Drive API to update doc with markdown content
            drive_service = self._drive_client()
            docs_service = self._client()

            # Get document for metadata
            doc = self._get_document(document_id, include_tabs=True, service=docs_service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)

            # Update file with markdown content using Drive API
            media = MediaInMemoryUpload(
                text.encode("utf-8"),
                mimetype="text/markdown",
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
        """Fetch document content as markdown.

        Note: Drive API markdown export exports the entire document and does not
        support tab-specific exports. If tab_id is provided, an error is raised.

        Args:
            document_id: The Google Doc ID
            tab_id: Must be None (tabs not supported)

        Raises:
            GoogleDocsError: If tab_id is not None
        """
        try:
            # Validate tab_id
            if tab_id is not None:
                raise GoogleDocsError(
                    "Tab-specific fetch operations are not supported. "
                    "Drive API markdown export exports the entire document. "
                    "Use tab_id=None to fetch the entire document."
                )

            # Use Drive API to export as markdown
            drive_service = self._drive_client()
            docs_service = self._client()

            # Get document metadata for title
            doc = self._get_document(document_id, include_tabs=True, service=docs_service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)

            # Export document as markdown using Drive API
            markdown_content = drive_service.files().export(
                fileId=document_id,
                mimeType="text/markdown"
            ).execute()

            # Decode bytes to string
            text = markdown_content.decode("utf-8") if isinstance(markdown_content, bytes) else markdown_content

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
