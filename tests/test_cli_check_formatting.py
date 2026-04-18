"""Tests for the yolo-doctor formatting helpers.

Exercise the splitter that turns a broker self-check blob into one
(title, detail) pair per problem.  Rendering is visual so we test the
structure, not the byte-exact output.
"""

from __future__ import annotations

from src.cli import _finalize_problem, _split_self_check_problems


def test_split_empty_output_returns_empty():
    assert _split_self_check_problems("") == []


def test_split_single_problem_with_continuation():
    blob = "FAIL: thing is broken.\nFix: do X.\nSee docs.\n"
    problems = _split_self_check_problems(blob)
    assert len(problems) == 1
    title, detail = problems[0]
    assert title == "thing is broken."
    assert detail == "Fix: do X.\nSee docs."


def test_split_two_problems_each_with_continuation():
    blob = (
        "FAIL: first problem.\n"
        "hint for first.\n"
        "FAIL: second problem.\n"
        "hint for second.\n"
        "extra line for second.\n"
    )
    problems = _split_self_check_problems(blob)
    assert len(problems) == 2
    assert problems[0] == ("first problem.", "hint for first.")
    assert problems[1] == (
        "second problem.",
        "hint for second.\nextra line for second.",
    )


def test_split_ignores_preamble_before_first_fail():
    blob = "Some preamble that is not a problem.\nFAIL: the real problem.\n"
    problems = _split_self_check_problems(blob)
    assert problems == [("the real problem.", "")]


def test_split_strips_blank_continuation_lines():
    blob = "FAIL: x.\n\n  \nhint.\n\n"
    problems = _split_self_check_problems(blob)
    assert problems == [("x.", "hint.")]


def test_finalize_problem_single_line():
    assert _finalize_problem(["only title"]) == ("only title", "")
