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
  * the run is below MAX_ATTEMPT, so a long outage cannot loop forever.

`decide` is the whole decision and is pure; `main` is the API calls around it (through `gh`).
"""
import json
import os
import subprocess
import sys

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


def decide(run, jobs, annotations_of):
    """Return (rerun, reason). annotations_of(job) is that job's check-run annotations."""
    if run.get("conclusion") not in ("failure", "cancelled"):
        return False, "the run concluded %s" % run.get("conclusion")
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

    rerun, reason = decide(run, jobs, annotations_of)
    print("%s run %s attempt %d: %s" % (run.get("name"), run_id, run["run_attempt"], reason))
    if rerun:
        subprocess.run(["gh", "api", "-X", "POST",
                        "repos/%s/actions/runs/%s/rerun-failed-jobs" % (repo, run_id)], check=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
