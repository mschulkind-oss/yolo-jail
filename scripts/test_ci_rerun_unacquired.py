#!/usr/bin/env python3
"""Regressions for ci_rerun_unacquired.py's decision, and the workflow step that calls it."""
import importlib.util
import os
from pathlib import Path
import re
import unittest

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("ci_rerun_unacquired", HERE / "ci_rerun_unacquired.py")
cr = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(cr)
WORKFLOW = HERE.parent / ".github" / "workflows" / "rerun-unacquired.yml"

# The annotation GitHub wrote on the dropped check-macos jobs of 2026-11-03.
DROPPED = [{"message": "The job was not acquired by Runner of type hosted even after multiple attempts"}]


def job(name, conclusion, steps=1, jid=None):
    return {"id": jid or abs(hash(name)) % 10**9, "name": name, "conclusion": conclusion,
            "steps": [{"name": "s"}] * steps}


def ann(table):
    return lambda j: table.get(j["name"], [])


RUN = {"conclusion": "failure", "run_attempt": 1}


class Decide(unittest.TestCase):
    def test_a_run_failed_only_by_a_dropped_job_is_rerun(self):
        jobs = [job("check-go", "success"), job("check-macos (1)", "cancelled", steps=0),
                job("check-macos (2)", "success")]
        rerun, reason = cr.decide(RUN, jobs, ann({"check-macos (1)": DROPPED}))
        self.assertTrue(rerun, reason)
        self.assertIn("check-macos (1)", reason)

    def test_a_real_failure_beside_a_dropped_job_is_not_rerun(self):
        jobs = [job("check-go", "failure"), job("check-macos (1)", "failure", steps=0)]
        rerun, reason = cr.decide(RUN, jobs, ann({"check-macos (1)": DROPPED}))
        self.assertFalse(rerun)
        self.assertIn("check-go (failure)", reason)

    def test_a_job_with_no_steps_but_no_annotation_is_not_dropped(self):
        # A run a human cancelled leaves unstarted jobs with no steps, and no such annotation.
        jobs = [job("check-macos (1)", "cancelled", steps=0)]
        rerun, _ = cr.decide({"conclusion": "cancelled", "run_attempt": 1}, jobs, ann({}))
        self.assertFalse(rerun)

    def test_a_job_that_ran_steps_is_never_dropped(self):
        jobs = [job("check-macos (1)", "failure", steps=3)]
        rerun, _ = cr.decide(RUN, jobs, ann({"check-macos (1)": DROPPED}))
        self.assertFalse(rerun)

    def test_the_last_attempt_is_not_rerun(self):
        jobs = [job("check-macos (1)", "failure", steps=0)]
        rerun, reason = cr.decide({"conclusion": "failure", "run_attempt": cr.MAX_ATTEMPT}, jobs,
                                  ann({"check-macos (1)": DROPPED}))
        self.assertFalse(rerun)
        self.assertIn("last", reason)

    def test_a_successful_run_is_left_alone(self):
        rerun, _ = cr.decide({"conclusion": "success", "run_attempt": 1}, [job("a", "success")], ann({}))
        self.assertFalse(rerun)

    def test_skipped_dependents_do_not_block_the_rerun(self):
        jobs = [job("install omp", "failure", steps=0), job("summary", "skipped", steps=0)]
        rerun, reason = cr.decide(RUN, jobs, ann({"install omp": DROPPED}))
        self.assertTrue(rerun, reason)


class Workflow(unittest.TestCase):
    def setUp(self):
        self.text = WORKFLOW.read_text()

    def test_the_workflow_calls_the_script(self):
        self.assertIn("python3 scripts/ci_rerun_unacquired.py", self.text)

    def test_it_watches_every_workflow_with_a_hosted_macos_job(self):
        watched = re.search(r"workflows:\s*\[([^\]]*)\]", self.text).group(1)
        names = {n.strip().strip("'\"") for n in watched.split(",")}
        hosted = 0
        for wf in sorted((HERE.parent / ".github" / "workflows").glob("*.yml")):
            body = wf.read_text()
            if re.search(r"runs-on:\s*macos-", body):
                hosted += 1
                name = re.search(r"^name:\s*(.+)$", body, re.M).group(1).strip()
                self.assertIn(name, names, "%s (%s) runs on hosted macOS and is not watched" % (wf.name, name))
        self.assertGreater(hosted, 0)

    def test_it_grants_only_actions_write_and_checks_read(self):
        perms = re.search(r"^permissions:\n((?:  .+\n)+)", self.text, re.M).group(1)
        got = {l.strip() for l in perms.splitlines() if l.strip()}
        self.assertEqual(got, {"actions: write", "checks: read"})

    def test_it_never_reruns_a_fork(self):
        self.assertIn("github.event.workflow_run.head_repository.full_name == github.repository", self.text)


if __name__ == "__main__":
    unittest.main(verbosity=1)
