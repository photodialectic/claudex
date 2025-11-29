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


def _extract_plain_text(document: dict) -> str:
    """Flatten Doc body content into a simple plaintext string."""
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
        except MissingGoogleCredentials as exc:
            raise exc
        except Exception as exc:  # pragma: no cover - safety net
            raise GoogleDocsError(f"Failed to initialize Docs client: {exc}") from exc

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
        service = self._client()
        try:
            doc = service.documents().create(body={"title": title}).execute()
            doc_id = doc["documentId"]
            tabs = doc.get("tabs") or []
            default_tab_id = None
            if tabs:
                default_tab_id = tabs[0].get("tabProperties", {}).get("tabId")
            if initial_text:
                requests, _ = build_markdown_requests(
                    initial_text,
                    start_index=1,
                    tab_id=default_tab_id,
                )
                if requests:
                    service.documents().batchUpdate(
                        documentId=doc_id, body={"requests": requests}
                    ).execute()
            return DocumentSummary(
                document_id=doc_id,
                title=doc.get("title"),
                url=_doc_url(doc_id),
                tab_id=default_tab_id,
            )
        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def append_text(
        self, document_id: str, text: str, tab_id: str | None = None
    ) -> DocumentSummary:
        try:
            service = self._client()
            doc = self._get_document(document_id, include_tabs=True, service=service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)
            body_source = self._tab_body(doc, tab)
            end_index = self._body_end_index(body_source)
            start_index = max(end_index - 1, 1)
            prepend_newline = end_index > 2
            requests, _ = build_markdown_requests(
                text,
                start_index=start_index,
                prepend_newline=prepend_newline,
                tab_id=effective_tab_id,
            )
            if requests:
                service.documents().batchUpdate(
                    documentId=document_id,
                    body={"requests": requests},
                ).execute()
            return DocumentSummary(
                document_id=document_id,
                title=doc.get("title"),
                url=_doc_url(document_id),
                tab_id=effective_tab_id,
            )
        except HttpError as exc:
            raise GoogleDocsError(exc.reason) from exc

    def replace_document(
        self, document_id: str, text: str, tab_id: str | None = None
    ) -> DocumentSummary:
        try:
            service = self._client()
            doc = self._get_document(document_id, include_tabs=True, service=service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)
            body_source = self._tab_body(doc, tab)
            end_index = self._body_end_index(body_source)
            requests: list[dict] = []
            if end_index > 1:
                requests.append(
                    {
                        "deleteContentRange": {
                            "range": self._create_delete_range(
                                effective_tab_id, end_index
                            )
                        }
                    }
                )
            markdown_requests, _ = build_markdown_requests(
                text,
                start_index=1,
                tab_id=effective_tab_id,
            )
            requests.extend(markdown_requests)
            if requests:
                service.documents().batchUpdate(
                    documentId=document_id, body={"requests": requests}
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
            service = self._client()
            doc = self._get_document(document_id, include_tabs=True, service=service)
            tab, effective_tab_id = self._resolve_tab(doc, tab_id)
            body_source = self._tab_body(doc, tab)
            text = _extract_plain_text(body_source)
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
