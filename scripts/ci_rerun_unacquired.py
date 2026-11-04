#!/usr/bin/env python3
"""Re-run a finished GitHub Actions run whose only problem is a job no runner ever took.

GitHub-hosted macOS arm64 runners run short at times. A job then waits for hours and is
dropped with the annotation "The job was not acquired by Runner of type hosted even after
multiple attempts", having run no step. Such a job says nothing about the code, but it fails
the run, and the release gate (tools/release-gate) needs every check-macos job green on the
exact commit. This script, run by .github/workflows/rerun-unacquired.yml when a watched
workflow finishes, re-runs the failed jobs of that run when, and only when:

  * at least one job was never acquired: no step ran, and its check run carries the annotation;
  * every other job that did not succeed was skipped (never a real failure: a job that ran and
    failed is a human's to read, and re-running it would hide it);
  * the run is below MAX_ATTEMPT, so a long outage cannot loop forever;
  * it is still the newest run of its workflow for its branch and event. ci.yml has one
    concurrency group per ref with cancel-in-progress, so a re-run of an older run would rejoin
    that group and cancel the newer run testing a newer commit. (For a watched workflow with no
    such group, the newer run already tests the newer code.)

A run the concurrency group cancelled because a newer push superseded it is never re-run: its
cancelled jobs carry GitHub's "Canceling since a higher priority waiting request ..." annotation,
not the not-acquired one, and the newer run exists.

`decide` is the whole decision and is pure; `main` is the API calls around it (through `gh`).
"""
import json
import os
import subprocess
import sys
import urllib.parse

NOT_ACQUIRED = "was not acquired by Runner"
# The attempt that just finished; at MAX_ATTEMPT no further re-run is asked for.
MAX_ATTEMPT = 3


def unacquired(job, annotations):
    """A job no runner took: it concluded without success, ran no step, and says so."""
    if job.get("conclusion") not in ("failure", "cancelled"):
        return False
    if job.get("steps"):
        return False
    return any(NOT_ACQUIRED in (a.get("message") or "") for a in annotations)


def superseded_by(run, newest):
    """The newer run that replaced this one in its concurrency group, or None."""
    if not newest or newest.get("id") == run.get("id"):
        return None
    if newest.get("head_sha") != run.get("head_sha") or \
            newest.get("run_number", 0) > run.get("run_number", 0):
        return newest
    return None


def decide(run, jobs, annotations_of, newest=None):
    """Return (rerun, reason). annotations_of(job) is that job's check-run annotations; newest is
    the newest run of the same workflow, branch and event (None when there is none to compare)."""
    if run.get("conclusion") not in ("failure", "cancelled"):
        return False, "the run concluded %s" % run.get("conclusion")
    newer = superseded_by(run, newest)
    if newer:
        return False, ("not re-run: run %s (#%s, %s) is newer on %s, and a re-run would cancel it"
                       % (newer.get("id"), newer.get("run_number"),
                          (newer.get("head_sha") or "")[:12], run.get("head_branch")))
    if run.get("run_attempt", 1) >= MAX_ATTEMPT:
        return False, "attempt %d is the last this workflow re-runs" % run.get("run_attempt", 1)
    dropped, other = [], []
    for job in jobs:
        conclusion = job.get("conclusion")
        if conclusion in ("success", "skipped", "neutral"):
            continue
        if unacquired(job, annotations_of(job)):
            dropped.append(job["name"])
        else:
            other.append("%s (%s)" % (job["name"], conclusion))
    if other:
        return False, "not re-run: a job that ran did not succeed: " + ", ".join(other)
    if not dropped:
        return False, "no job was dropped for want of a runner"
    return True, "re-running; no runner took: " + ", ".join(dropped)


def gh_json(*args):
    out = subprocess.run(["gh", "api", *args], check=True, capture_output=True, text=True).stdout
    return json.loads(out)


def main():
    repo = os.environ["REPO"]
    run_id = os.environ["RUN_ID"]
    run = gh_json("repos/%s/actions/runs/%s" % (repo, run_id))
    jobs = []
    page = 1
    while True:
        got = gh_json("repos/%s/actions/runs/%s/attempts/%d/jobs?per_page=100&page=%d"
                      % (repo, run_id, run["run_attempt"], page))
        jobs += got["jobs"]
        if len(got["jobs"]) < 100:
            break
        page += 1

    def annotations_of(job):
        return gh_json("repos/%s/check-runs/%d/annotations" % (repo, job["id"]))

    # The newest run of this workflow for the same branch and event: the API lists newest first.
    newest = gh_json("repos/%s/actions/workflows/%d/runs?branch=%s&event=%s&per_page=1"
                     % (repo, run["workflow_id"], urllib.parse.quote(run["head_branch"], safe=""),
                        run["event"]))["workflow_runs"]
    if not newest:
        # The run itself should be listed; a listing without it cannot show it is the newest.
        print("%s run %s: not re-run: the newest run of its branch could not be read"
              % (run.get("name"), run_id))
        return 0

    rerun, reason = decide(run, jobs, annotations_of, newest[0])
    print("%s run %s attempt %d: %s" % (run.get("name"), run_id, run["run_attempt"], reason))
    if rerun:
        subprocess.run(["gh", "api", "-X", "POST",
                        "repos/%s/actions/runs/%s/rerun-failed-jobs" % (repo, run_id)], check=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
