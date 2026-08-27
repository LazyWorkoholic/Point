r"""web_search -- provider-agnostic, keyless by default (B10).

**The implementation chosen, and its cost:** DuckDuckGo's keyless HTML
endpoint (`https://html.duckduckgo.com/html/`), parsed with the standard
library's `html.parser`. Rationale, recorded here because B10's handoff
asks for it:

  * **No key.** Point is local-first and offline-capable; a v1 tool that
    needs an account before it works is a tool most users never see
    working. Brave Search and Tavily are both better search, and both
    start with "get an API key".
  * **No new dependency.** `httpx` is already a dependency (B4's Ollama
    adapter), and `html.parser` is stdlib. B10's rule is no new
    dependency without a reason in the handoff; this needed none.
  * **Cost: zero per query, and no rate-limit contract.** That is the
    real trade. DuckDuckGo publishes no quota for this endpoint, does not
    promise the markup will not change, and will throttle a burst. Every
    one of those failure modes is a `ToolResult(ok=False)` the model can
    read, never an exception -- so the worst case is an agent that is
    told search is unavailable and says so, which is the correct
    behaviour for a local-first tool anyway.

`SearchBackend` is the seam: the parsing and the HTTP call sit behind one
callable, so swapping in a keyed backend later is a new function and a
config key, not a rewrite of the tool. A keyed backend would resolve its
key through B6's credential chain (`appconfig.resolve_api_key`) like
every other key -- there is no second key path.
"""

from __future__ import annotations

import html
import urllib.parse
from dataclasses import dataclass
from html.parser import HTMLParser
from typing import Any

import httpx

from logging_setup import get_logger
from tools.models import Tool, ToolResult

_log = get_logger("tools.web")

_ENDPOINT = "https://html.duckduckgo.com/html/"
# A browser-ish UA. The endpoint serves an empty page to an obviously
# scripted client; this is not evasion, it is the difference between the
# tool working and the tool returning nothing at all.
_HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/124.0 Safari/537.36"
    ),
    "Accept-Language": "en-US,en;q=0.9",
}
_TIMEOUT_S = 20.0

WEB_SEARCH_SCHEMA = {
    "type": "object",
    "properties": {
        "query": {"type": "string", "description": "What to search for."},
        "limit": {
            "type": "integer",
            "description": "Maximum number of results to return.",
            "default": 5,
            "minimum": 1,
            "maximum": 20,
        },
    },
    "required": ["query"],
}


@dataclass(frozen=True)
class SearchResult:
    title: str
    url: str
    snippet: str


class _ResultParser(HTMLParser):
    """Pulls (title, url, snippet) triples out of the HTML endpoint's
    result list. Written against class names rather than document
    structure, because the structure is what changes first."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.results: list[SearchResult] = []
        self._pending: dict[str, str] = {}
        self._capture: str | None = None

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attributes = {k: (v or "") for k, v in attrs}
        classes = attributes.get("class", "").split()
        if tag == "a" and "result__a" in classes:
            self._flush()
            self._pending = {"url": _clean_url(attributes.get("href", "")), "title": "", "snippet": ""}
            self._capture = "title"
        elif tag == "a" and "result__snippet" in classes:
            self._capture = "snippet"

    def handle_endtag(self, tag: str) -> None:
        if tag == "a" and self._capture is not None:
            if self._capture == "snippet":
                self._flush()
            self._capture = None

    def handle_data(self, data: str) -> None:
        if self._capture and self._pending:
            self._pending[self._capture] += data

    def close(self) -> None:  # noqa: D102
        super().close()
        self._flush()

    def _flush(self) -> None:
        pending = self._pending
        self._pending = {}
        if pending.get("url") and pending.get("title"):
            self.results.append(
                SearchResult(
                    title=" ".join(pending["title"].split()),
                    url=pending["url"],
                    snippet=" ".join(pending.get("snippet", "").split()),
                )
            )


def _clean_url(href: str) -> str:
    """DuckDuckGo wraps result links in its own redirector with the real
    target in `uddg`. A model handed the redirector cannot tell one
    domain from another, which defeats the point of returning urls."""
    unescaped = html.unescape(href)
    if "duckduckgo.com/l/" not in unescaped and not unescaped.startswith("//duckduckgo.com/l/"):
        return unescaped
    query = urllib.parse.urlparse(unescaped).query
    target = urllib.parse.parse_qs(query).get("uddg")
    return target[0] if target else unescaped


async def search_duckduckgo(query: str, limit: int) -> list[SearchResult]:
    async with httpx.AsyncClient(timeout=_TIMEOUT_S, follow_redirects=True) as client:
        response = await client.post(_ENDPOINT, data={"q": query}, headers=_HEADERS)
    response.raise_for_status()
    parser = _ResultParser()
    parser.feed(response.text)
    parser.close()
    return parser.results[:limit]


# The seam. Swapping in a keyed backend is reassigning this, not
# rewriting the tool.
SearchBackend = search_duckduckgo


async def _web_search(arguments: dict[str, Any], ctx: Any) -> ToolResult:
    query = arguments["query"].strip()
    if not query:
        return ToolResult.failure("query is empty")
    try:
        results = await SearchBackend(query, arguments["limit"])
    except httpx.HTTPStatusError as exc:
        return ToolResult.failure(
            f"search is unavailable (HTTP {exc.response.status_code}); "
            "answer from what you already know, or say you could not search"
        )
    except httpx.HTTPError as exc:
        _log.warning("web_search failed: %s", exc)
        return ToolResult.failure(
            f"search is unavailable ({type(exc).__name__}); answer from what "
            "you already know, or say you could not search"
        )

    if not results:
        return ToolResult.success("", count=0, query=query)
    rendered = "\n\n".join(
        f"{index}. {r.title}\n   {r.url}\n   {r.snippet}" if r.snippet
        else f"{index}. {r.title}\n   {r.url}"
        for index, r in enumerate(results, start=1)
    )
    return ToolResult.success(rendered, count=len(results), query=query)


WEB_SEARCH = Tool(
    name="web_search",
    description=(
        "Search the web. Returns a numbered list of results, each with a "
        "title, url, and snippet. Snippets are summaries, not sources -- "
        "read a page before relying on what it says."
    ),
    schema=WEB_SEARCH_SCHEMA,
    timeout_s=30,
    run=_web_search,
)
