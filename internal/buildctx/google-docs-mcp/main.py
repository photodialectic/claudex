from __future__ import annotations

from argparse import ArgumentParser

from fastapi import Depends, FastAPI, HTTPException, Query, Request, status
from fastapi.concurrency import run_in_threadpool
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import HTMLResponse, JSONResponse
from fastmcp import FastMCP
import uvicorn

from google_docs_mcp import schemas
from google_docs_mcp.auth import GoogleAuthManager, auth_manager
from google_docs_mcp.docs import (
    DocumentContent,
    DocumentSummary,
    GoogleDocsService,
    docs_service,
)
from google_docs_mcp.exceptions import (
    AuthorizationFlowNotStarted,
    CredentialsFileMissing,
    GoogleAuthError,
    GoogleDocsError,
    MissingGoogleCredentials,
)
from google_docs_mcp.config import Settings, settings


# --------------------------------------------------------------------------- #
# MCP server setup
# --------------------------------------------------------------------------- #
mcp_server = FastMCP(
    name=settings.project_name,
    instructions=settings.instructions,
)
mcp_http_app = mcp_server.http_app(path="/")


def create_app() -> FastAPI:
    api = FastAPI(
        title=settings.project_name,
        version="0.1.0",
        description="FastAPI wrapper exposing Google Docs tools via MCP and REST.",
        lifespan=mcp_http_app.lifespan,
    )
    api.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_allow_origins or ["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
    return api


app = create_app()


def get_auth_manager() -> GoogleAuthManager:
    return auth_manager


def get_docs_service() -> GoogleDocsService:
    return docs_service


@app.get("/health")
async def health(auth: GoogleAuthManager = Depends(get_auth_manager)):
    return {
        "status": "ok",
        "oauth_client": str(settings.credentials_file),
        "has_credentials": auth.have_credentials(),
    }


@app.get("/auth/status", response_model=schemas.AuthStatusResponse)
async def auth_status(auth: GoogleAuthManager = Depends(get_auth_manager)):
    return schemas.AuthStatusResponse(
        authenticated=auth.have_credentials(),
        credentials_file=str(settings.credentials_file),
        token_file=str(settings.token_file),
        redirect_uri=settings.redirect_uri,
    )


@app.post("/auth/start", response_model=schemas.AuthStartResponse)
async def auth_start(auth: GoogleAuthManager = Depends(get_auth_manager)):
    def _start():
        url, state = auth.start_authorization()
        return url, state

    try:
        url, state = await run_in_threadpool(_start)
    except CredentialsFileMissing as exc:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)
        ) from exc
    return schemas.AuthStartResponse(
        authorization_url=url,
        state=state,
        redirect_uri=settings.redirect_uri,
        scopes=settings.scopes,
    )


@app.get("/auth/callback", response_class=HTMLResponse)
async def auth_callback(
    request: Request,
    state: str = Query(...),
    code: str = Query(...),
    auth: GoogleAuthManager = Depends(get_auth_manager),
):
    async def _finish():
        return await run_in_threadpool(auth.finish_authorization, state, code)

    try:
        await _finish()
    except AuthorizationFlowNotStarted as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc))

    message = """
    <html>
      <body>
        <h2>Google account linked ✅</h2>
        <p>You can return to your Claudex session. The MCP tools now share access to Google Docs.</p>
      </body>
    </html>
    """
    return HTMLResponse(content=message, status_code=status.HTTP_200_OK)


@app.post("/docs", response_model=schemas.DocumentSummaryResponse)
async def create_document(
    payload: schemas.CreateDocumentRequest,
    service: GoogleDocsService = Depends(get_docs_service),
):
    summary = await run_in_threadpool(
        service.create_document, payload.title, payload.initial_text
    )
    return schemas.DocumentSummaryResponse(**summary.as_dict())


@app.post(
    "/docs/{document_id}/append", response_model=schemas.DocumentSummaryResponse
)
async def append_document(
    document_id: str,
    payload: schemas.AppendDocumentRequest,
    service: GoogleDocsService = Depends(get_docs_service),
):
    summary = await run_in_threadpool(
        service.append_text,
        document_id,
        payload.text,
        payload.tab_id,
    )
    return schemas.DocumentSummaryResponse(**summary.as_dict())


@app.put("/docs/{document_id}", response_model=schemas.DocumentSummaryResponse)
async def replace_document(
    document_id: str,
    payload: schemas.ReplaceDocumentRequest,
    service: GoogleDocsService = Depends(get_docs_service),
):
    summary = await run_in_threadpool(
        service.replace_document,
        document_id,
        payload.text,
        payload.tab_id,
    )
    return schemas.DocumentSummaryResponse(**summary.as_dict())


@app.get("/docs/{document_id}", response_model=schemas.DocumentContentResponse)
async def get_document(
    document_id: str,
    tab_id: str | None = Query(
        default=None, description="Target tab ID. Defaults to the first tab."
    ),
    service: GoogleDocsService = Depends(get_docs_service),
):
    doc = await run_in_threadpool(service.fetch_document, document_id, tab_id)
    return schemas.DocumentContentResponse(**doc.as_dict())


@app.get(
    "/docs/{document_id}/tabs",
    response_model=list[schemas.TabMetadataResponse],
)
async def list_tabs(
    document_id: str,
    service: GoogleDocsService = Depends(get_docs_service),
):
    tabs = await run_in_threadpool(service.list_tabs, document_id)
    return [tab.as_dict() for tab in tabs]


@app.exception_handler(GoogleAuthError)
async def auth_error_handler(_: Request, exc: GoogleAuthError):
    return JSONResponse(
        status_code=status.HTTP_400_BAD_REQUEST,
        content={"error": exc.__class__.__name__, "detail": str(exc)},
    )


@app.exception_handler(GoogleDocsError)
async def docs_error_handler(_: Request, exc: GoogleDocsError):
    return JSONResponse(
        status_code=status.HTTP_400_BAD_REQUEST,
        content={"error": "GoogleDocsError", "detail": str(exc)},
    )


@mcp_server.tool(
    name="start_google_auth_flow",
    description="Generate an OAuth consent URL to link a Google account.",
)
def start_google_auth_flow():
    url, state = auth_manager.start_authorization()
    return {
        "authorization_url": url,
        "state": state,
        "redirect_uri": settings.redirect_uri,
        "instructions": (
            "Open the authorization_url in a browser, grant access, and wait for "
            "the success page before using other tools."
        ),
    }


@mcp_server.tool(
    name="check_google_auth_status",
    description="Return whether cached Google credentials are available.",
)
def check_google_auth_status():
    return {
        "authenticated": auth_manager.have_credentials(),
        "credentials_file": str(settings.credentials_file),
        "token_file": str(settings.token_file),
        "redirect_uri": settings.redirect_uri,
    }


@mcp_server.tool(
    name="create_google_doc",
    description="Create a new Google Doc and optionally seed it with text.",
)
def create_google_doc(title: str, initial_text: str | None = None):
    summary: DocumentSummary = docs_service.create_document(title, initial_text)
    return summary.as_dict()


@mcp_server.tool(
    name="append_google_doc",
    description="Append plain text at the end of a Google Doc.",
)
def append_google_doc(document_id: str, text: str, tab_id: str | None = None):
    summary: DocumentSummary = docs_service.append_text(document_id, text, tab_id)
    return summary.as_dict()


@mcp_server.tool(
    name="replace_google_doc",
    description="Replace the entire body of a Google Doc with new text.",
)
def replace_google_doc(document_id: str, text: str, tab_id: str | None = None):
    summary: DocumentSummary = docs_service.replace_document(document_id, text, tab_id)
    return summary.as_dict()


@mcp_server.tool(
    name="read_google_doc",
    description="Return the plain-text contents of a Google Doc.",
)
def read_google_doc(document_id: str, tab_id: str | None = None):
    content: DocumentContent = docs_service.fetch_document(document_id, tab_id)
    return content.as_dict()


@mcp_server.tool(
    name="list_google_doc_tabs",
    description="List available tabs for a Google Doc.",
)
def list_google_doc_tabs(document_id: str):
    tabs = docs_service.list_tabs(document_id)
    return [tab.as_dict() for tab in tabs]


# Expose the MCP HTTP transport at /mcp so clients can connect over SSE/streamable HTTP.
app.mount("/mcp", mcp_http_app)


def main():
    """
    Convenience entrypoint for running the FastAPI server locally.
    """
    parser = ArgumentParser(description="Google Docs MCP server")
    parser.add_argument(
        "--stdio",
        action="store_true",
        help="Run the MCP server over stdio instead of HTTP (no FastAPI endpoints).",
    )
    parser.add_argument(
        "--host",
        default=settings.host,
        help="Bind host for the HTTP server (default from settings).",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=settings.port,
        help="Bind port for the HTTP server (default from settings).",
    )
    args = parser.parse_args()

    if args.stdio:
        mcp_server.run(transport="stdio")
        return

    uvicorn.run(
        "main:app",
        host=args.host,
        port=args.port,
        reload=False,
    )


if __name__ == "__main__":
    main()
