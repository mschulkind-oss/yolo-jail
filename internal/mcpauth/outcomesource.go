package mcpauth

func (b *OnceBroker) observeSourceProblem(src OnceSource, server OnceServer) error {
	snapshot := src.Store.Snapshot()
	if snapshot.State != SourceMalformed && snapshot.State != SourceUnsafe {
		return nil
	}
	release, err := b.lockRecord(server)
	if err != nil {
		return err
	}
	defer release()
	snapshot = src.Store.Snapshot()
	code := OutcomeCode("")
	switch snapshot.State {
	case SourceMalformed:
		code = OutcomeSourceMalformed
	case SourceUnsafe:
		code = OutcomeSourceUnsafe
	default:
		return nil
	}
	generation := uint64(0)
	if record, err := b.findRecord(server); err == nil && record != nil {
		generation = record.Generation
	}
	b.writeOutcomeLocked(server, "", code, generation)
	return nil
}

func recordNativeName(src *OnceSource, record *Record) string {
	if src == nil || record == nil {
		return ""
	}
	return src.nativeName(record.SourceKey, record.Entry)
}
