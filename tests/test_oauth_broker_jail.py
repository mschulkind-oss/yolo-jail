"""Tests for src.oauth_broker_jail — the in-jail TLS terminator.

The big regression we lock in here: only ``grant_type=refresh_token``
requests should route through the host broker's refresh flow.  Every
other grant (most importantly ``authorization_code`` from ``/login``)
and any non-``/v1/oauth/token`` path must pass through to upstream, or
``/login`` returns 400 with ``no_refresh_token`` on a logged-out jail.
"""

from __future__ import annotations

import json

from src import oauth_broker_jail


# ---------------------------------------------------------------------------
# _is_refresh_grant — the routing predicate
# ---------------------------------------------------------------------------


def test_is_refresh_grant_true_for_refresh_token():
    body = json.dumps({"grant_type": "refresh_token", "refresh_token": "abc"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is True


def test_is_refresh_grant_false_for_authorization_code():
    """/login posts ``grant_type=authorization_code`` — the routing bug
    treated this as a refresh and returned 400.  Must route to the
    proxy, not the broker."""
    body = json.dumps({"grant_type": "authorization_code", "code": "xyz"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is False


def test_is_refresh_grant_false_for_empty_body():
    assert oauth_broker_jail._is_refresh_grant(b"") is False


def test_is_refresh_grant_false_for_non_json_body():
    """A malformed body (e.g. form-urlencoded) must not accidentally
    match — let upstream return its own error."""
    assert oauth_broker_jail._is_refresh_grant(b"grant_type=refresh_token") is False


def test_is_refresh_grant_false_for_json_non_object():
    assert oauth_broker_jail._is_refresh_grant(b'"refresh_token"') is False
    assert oauth_broker_jail._is_refresh_grant(b"[]") is False


def test_is_refresh_grant_false_when_grant_type_missing():
    body = json.dumps({"refresh_token": "abc"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is False
