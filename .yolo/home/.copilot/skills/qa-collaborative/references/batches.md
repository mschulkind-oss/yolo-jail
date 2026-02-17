# QA Batching Protocol

## Creating a Batch
1. Select a group of related flows (e.g., "Auth & Onboarding").
2. Create `docs/qa/batch-<N>.md`.
3. Include for each scenario:
   - **ID**: `FLOW-XXX`
   - **Description**: What to do.
   - **Status**: `⬜ To Be Tested`
   - **Notes**: Any agent-pre-verification findings.

## Presenting to the Human
- State clearly: "I have prepared Batch <N> in `docs/qa/batch-<N>.md`. Please review and update the status of each item. Let me know when you are done."
- **Wait**: Do not proceed until the human provides feedback.

## Processing Feedback
1. **Read**: Check the status of each item in the batch file.
2. **Act**: 
   - For `✅ Pass`: Update the tracker.
   - For `❌ Fail / 🐛 Bug`: Investigate and fix immediately. Verify the fix.
3. **Close**: Update `docs/qa/user-flow-tracker.md` with final results from this batch.
4. **Continue**: Propose the next batch of work.