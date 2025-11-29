from __future__ import annotations

from pathlib import Path
from threading import Lock
from typing import Dict, Tuple

from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials
from google_auth_oauthlib.flow import Flow

from .config import Settings, settings
from .exceptions import (
    AuthorizationFlowNotStarted,
    CredentialsFileMissing,
    MissingGoogleCredentials,
)


class GoogleAuthManager:
    """
    Handles OAuth flows and cached credential storage for Google APIs.
    """

    def __init__(self, config: Settings | None = None):
        self.config = config or settings
        self._pending_flows: Dict[str, Flow] = {}
        self._lock = Lock()

    # ------------------------------------------------------------------ #
    # Credential helpers
    # ------------------------------------------------------------------ #
    def _load_cached_credentials(self) -> Credentials | None:
        token_file: Path = self.config.token_file
        if not token_file.exists():
            return None
        creds = Credentials.from_authorized_user_file(
            str(token_file), scopes=self.config.scopes
        )
        if creds.expired and creds.refresh_token:
            creds.refresh(Request())
            self._persist_credentials(creds)
        return creds

    def _persist_credentials(self, creds: Credentials) -> None:
        data = creds.to_json()
        self.config.token_file.write_text(data, encoding="utf-8")

    def ensure_client_file(self) -> None:
        if not self.config.credentials_file.exists():
            raise CredentialsFileMissing(
                f"Missing OAuth client file at {self.config.credentials_file}"
            )

    def have_credentials(self) -> bool:
        return self.config.token_file.exists()

    def require_credentials(self) -> Credentials:
        creds = self._load_cached_credentials()
        if creds is None:
            raise MissingGoogleCredentials(
                "Authorize the server first by visiting /auth/start"
            )
        return creds

    # ------------------------------------------------------------------ #
    # OAuth flow management
    # ------------------------------------------------------------------ #
    def start_authorization(self) -> Tuple[str, str]:
        """
        Begin an OAuth flow and return (authorization_url, state).
        """
        self.ensure_client_file()

        flow = Flow.from_client_secrets_file(
            str(self.config.credentials_file),
            scopes=self.config.scopes,
            redirect_uri=self.config.redirect_uri,
        )
        auth_url, state = flow.authorization_url(
            include_granted_scopes="true",
            access_type="offline",
            prompt="consent",
        )
        with self._lock:
            self._pending_flows[state] = flow
        return auth_url, state

    def finish_authorization(self, state: str, code: str) -> Credentials:
        """
        Exchange the auth code for persisted credentials.
        """
        with self._lock:
            flow = self._pending_flows.pop(state, None)
        if flow is None:
            raise AuthorizationFlowNotStarted(
                "Unknown OAuth state. Start a new flow from /auth/start."
            )
        flow.fetch_token(code=code)
        creds = flow.credentials
        self._persist_credentials(creds)
        return creds


auth_manager = GoogleAuthManager()

