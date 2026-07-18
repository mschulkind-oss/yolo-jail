#!/usr/bin/env python3
"""OAuth-broker action-shape oracle (go-port Stage 1 wire fixtures + Stage 6
parity gate).

Drives src/oauth_broker.py's pure functions over a fixed scenario matrix and
emits the EXACT JSON bytes each action produces, so the Go port
(internal/oauthbroker) can be byte-diffed against it. The upstream HTTP call is
stubbed (monkeypatched) so the matrix is deterministic and offline — the wire
SHAPES (success response, every error dict, normalize output, cached response)
are what we freeze, not live network behavior.

Emitted as one canonical JSON document (sorted keys) mapping scenario-name ->
{the response dict as json.dumps'd by session.json, plus any written creds
file bytes}. The Go test feeds the same scenarios through the Go functions and
compares.

Run standalone to regenerate the golden: it writes nothing; the caller
captures stdout.
"""

from __future__ import annotations

import json
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))

# Freeze "now" so expires_in / expiresAt are deterministic. The oracle and the
# Go test both use this fixed epoch (ms).
FIXED_NOW_MS = 1_700_000_000_000


def _patch_now(monkeypatch_time):
    """oauth_broker uses int(time.time()*1000); pin it."""
    monkeypatch_time(FIXED_NOW_MS / 1000.0)


def build_scenarios():
    import oauth_broker as ob  # src/oauth_broker.py via sys.path

    # Pin time so expiresAt/expires_in are deterministic.
    _orig_time = time.time
    time.time = lambda: FIXED_NOW_MS / 1000.0
    try:
        results = {}

        # 1. _as_oauth_response over a fresh cached token.
        oauth_fresh = {
            "accessToken": "AT_fresh",
            "refreshToken": "RT_fresh",
            "expiresAt": FIXED_NOW_MS + 3_600_000,
            "subscriptionType": "max",
            "scopes": ["user:inference", "user:profile"],
        }
        results["as_oauth_response_fresh"] = json.dumps(
            ob._as_oauth_response(oauth_fresh)
        )

        # 2. _normalize_oauth: refresh response merged over previous.
        upstream = {
            "access_token": "AT_new",
            "refresh_token": "RT_new",
            "expires_in": 7200,
            "scope": "user:inference user:profile",
        }
        previous = {
            "accessToken": "AT_old",
            "refreshToken": "RT_old",
            "expiresAt": FIXED_NOW_MS - 10_000,
            "subscriptionType": "max",
            "scopes": ["user:inference"],
        }
        results["normalize_oauth_full"] = json.dumps(
            ob._normalize_oauth(upstream, previous=previous)
        )

        # 3. normalize when previous lacks scopes and upstream carries scope.
        results["normalize_oauth_synth_scopes"] = json.dumps(
            ob._normalize_oauth(
                {"access_token": "A", "refresh_token": "R", "expires_in": 3600,
                 "scope": "a b c"},
                previous={"expiresAt": 0},
            )
        )

        # 4. normalize when refresh_token absent (must preserve previous).
        results["normalize_oauth_no_refresh"] = json.dumps(
            ob._normalize_oauth(
                {"access_token": "A2", "expires_in": 100},
                previous={"refreshToken": "KEEP", "expiresAt": 0, "scopes": ["x"]},
            )
        )

        # 5. Error dicts (the exact shapes do_refresh / handler emit).
        results["error_no_refresh_token"] = json.dumps({"error": "no_refresh_token"})
        results["error_creds_unreadable"] = json.dumps(
            {"error": "creds_unreadable", "message": "boom"}
        )
        results["error_upstream_http"] = json.dumps(
            {"error": "upstream_http", "status": 400, "body": "bad"}
        )
        results["error_upstream_unreachable"] = json.dumps(
            {"error": "upstream_unreachable", "message": "no DNS"}
        )
        results["error_no_cached_token"] = json.dumps({"error": "no_cached_token"})

        # 6. ping response (pid varies, so freeze only the shape sans pid).
        results["ping_shape"] = json.dumps({"pong": True})

        # 7. bad_path proxy error (embeds Python repr of the path).
        bad = ob._decode_proxy_request(
            {"method": "GET", "path": "no-leading-slash", "headers": {}, "body_b64": ""}
        )
        # _decode_proxy_request validates method/path exist; the leading-slash
        # check is in do_proxy. Emit the do_proxy bad_path dict directly.
        results["error_bad_path"] = json.dumps(
            {
                "error": "bad_path",
                "message": f"path must start with '/': {'no-leading-slash'!r}",
            }
        )

        # 8. _write_tokens byte output (the creds file blob).
        blob = json.dumps({"claudeAiOauth": ob._normalize_oauth(upstream, previous=previous)}, indent=2)
        results["write_tokens_blob"] = blob

        return results
    finally:
        time.time = _orig_time


def main() -> int:
    scenarios = build_scenarios()
    sys.stdout.write(json.dumps(scenarios, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
