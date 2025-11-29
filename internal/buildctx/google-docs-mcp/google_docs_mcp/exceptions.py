class GoogleAuthError(RuntimeError):
    """Base error raised for Google auth failures."""


class CredentialsFileMissing(GoogleAuthError):
    """Raised when the configured OAuth client file does not exist."""


class AuthorizationFlowNotStarted(GoogleAuthError):
    """Raised when a callback is attempted for an unknown OAuth state."""


class MissingGoogleCredentials(GoogleAuthError):
    """Raised when Google API access is attempted without cached credentials."""


class GoogleDocsError(RuntimeError):
    """Generic error raised when the Docs API reports a failure."""

