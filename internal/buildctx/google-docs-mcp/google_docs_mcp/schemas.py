from __future__ import annotations

from pydantic import BaseModel, Field


class CreateDocumentRequest(BaseModel):
    title: str = Field(..., description="Title for the Google Doc.")
    initial_text: str | None = Field(
        default=None, description="Optional initial content inserted at the top."
    )


class AppendDocumentRequest(BaseModel):
    text: str = Field(..., description="Plain text that should be appended.")
    tab_id: str | None = Field(
        default=None, description="Target tab ID. Defaults to the first tab."
    )


class ReplaceDocumentRequest(BaseModel):
    text: str = Field(..., description="Plain text that replaces the document content.")
    tab_id: str | None = Field(
        default=None, description="Target tab ID. Defaults to the first tab."
    )


class DocumentSummaryResponse(BaseModel):
    document_id: str
    title: str | None = None
    url: str
    tab_id: str | None = None


class DocumentContentResponse(DocumentSummaryResponse):
    content: str


class AuthStartResponse(BaseModel):
    authorization_url: str
    state: str
    redirect_uri: str
    scopes: list[str]


class AuthStatusResponse(BaseModel):
    authenticated: bool
    credentials_file: str
    token_file: str
    redirect_uri: str


class TabMetadataResponse(BaseModel):
    tab_id: str | None = None
    title: str | None = None
    depth: int
    parent_id: str | None = None
