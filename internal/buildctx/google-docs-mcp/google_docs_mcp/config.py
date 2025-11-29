from __future__ import annotations

from pathlib import Path
from typing import List, Optional

from pydantic import AnyHttpUrl, Field
from pydantic_settings import BaseSettings, SettingsConfigDict

HOST_CONFIG_DIR = Path("/home/node/.claudex")


class Settings(BaseSettings):
    """Runtime configuration for the Google Docs MCP server."""

    project_name: str = Field(
        default="Claudex Google Docs MCP",
        description="Human friendly name used by FastAPI and MCP metadata.",
    )
    instructions: str = Field(
        default=(
            "Tools for creating, reading, and updating Google Docs in the "
            "signed-in workspace."
        ),
        description="High level instructions that MCP clients display to users.",
    )

    host: str = Field(
        default="0.0.0.0",
        alias="MCP_SERVER_HOST",
        description="Interface FastAPI should bind to.",
    )
    port: int = Field(
        default=8810,
        alias="MCP_SERVER_PORT",
        description="Port FastAPI should listen on.",
    )
    external_base_url: Optional[AnyHttpUrl] = Field(
        default=None,
        alias="MCP_PUBLIC_BASE_URL",
        description=(
            "Public URL used when generating OAuth redirect URIs. "
            "Falls back to http://localhost:<port> if unset."
        ),
    )

    credentials_file: Path = Field(
        default=(HOST_CONFIG_DIR / "google_oauth_client.json"),
        alias="GOOGLE_CLIENT_CREDENTIALS",
        description="Path to the OAuth client credentials JSON downloaded from GCP.",
    )
    token_file: Path = Field(
        default=(HOST_CONFIG_DIR / "google-docs-token.json"),
        alias="GOOGLE_TOKEN_CACHE",
        description="Where refresh/access tokens are stored after OAuth completes.",
    )
    scopes: List[str] = Field(
        default_factory=lambda: [
            "https://www.googleapis.com/auth/documents",
            "https://www.googleapis.com/auth/drive.file",
        ],
        alias="GOOGLE_OAUTH_SCOPES",
        description="OAuth scopes requested during consent.",
    )

    cors_allow_origins: List[str] = Field(
        default_factory=lambda: ["http://localhost", "http://127.0.0.1"],
        alias="MCP_CORS_ALLOW_ORIGINS",
        description="Origins allowed to access the FastAPI app.",
    )

    model_config = SettingsConfigDict(
        env_prefix="",
        env_file=".env",
        env_file_encoding="utf-8",
        extra="allow",
    )

    @property
    def redirect_uri(self) -> str:
        base = (
            str(self.external_base_url)
            if self.external_base_url is not None
            else f"http://localhost:{self.port}"
        )
        return f"{base.rstrip('/')}/auth/callback"

    def ensure_cache_paths(self) -> None:
        for path in {self.token_file.parent, self.credentials_file.parent}:
            if not path or path == Path("."):
                continue
            path.mkdir(parents=True, exist_ok=True)


settings = Settings()
settings.ensure_cache_paths()
