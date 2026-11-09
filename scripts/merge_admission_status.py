"""Publish merge admission results with a dedicated credential, never a PR token."""
import json
import re
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


class StatusPublisherError(ValueError):
    """The trusted status publisher is unavailable or misconfigured."""


class GitHubStatusPublisher:
    """GitHub status writes require their own scoped App credential."""

    def __init__(self, repository, token, read_token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise StatusPublisherError("invalid status repository")
        if not token or token == read_token:
            raise StatusPublisherError("trusted status credential is missing or not isolated")
        self.endpoint = "https://api.github.com/repos/" + repository
        self.token = token

    def status(self, sha, state, description):
        if not re.fullmatch(r"[0-9a-f]{40}", str(sha)):
            raise StatusPublisherError("invalid status candidate")
        if state not in ("pending", "success", "failure"):
            raise StatusPublisherError("invalid status state")
        request = Request(
            self.endpoint + "/statuses/" + sha,
            data=json.dumps({
                "state": state, "context": "flowstate/merge-admission",
                "description": description[:140],
            }).encode(),
            method="POST",
            headers={
                "Authorization": "Bearer " + self.token,
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2022-11-28",
                "Content-Type": "application/json",
            },
        )
        try:
            with urlopen(request, timeout=15):
                pass
        except (HTTPError, URLError, TimeoutError) as error:
            raise StatusPublisherError("trusted GitHub status publication failed") from error
