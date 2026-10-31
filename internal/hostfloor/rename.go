package hostfloor

// RenamePack respells the pack each program record names where rename maps it: `yolo config
// migrate` renames a pack it converts to its readable handle (OQ-RC15), and a prepared install is
// accepted only for the pack its record names (preparedRecordServes). Each record is rewritten
// under its program's install lock, so an install in flight is waited for. It returns one line
// per record rewritten.
func (f *Floor) RenamePack(rename map[string]string) ([]string, error) {
	var moved []string
	for bin, rec := range f.Records() {
		to, ok := rename[rec.Pack]
		if !ok {
			continue
		}
		lk, err := acquire(f.lockPath(bin), true, nil)
		if err != nil {
			return moved, err
		}
		cur, err := f.readRecord(bin)
		if err == nil && cur.Pack == rec.Pack {
			from := cur.Pack
			cur.Pack = to
			err = f.writeRecord(cur)
			if err == nil {
				moved = append(moved, "host floor record "+bin+": "+from+" → "+to)
			}
		}
		lk.release()
		if err != nil {
			return moved, err
		}
	}
	return moved, nil
}
