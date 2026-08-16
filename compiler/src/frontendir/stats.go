package frontendir

// BundleStats reports a bundle's structural facts without reconstructing the
// AST. It exists so a second implementation's reading of the same file can be
// checked against stage0's — the interoperability the v2 schema is for is only
// meaningful if both sides can be made to agree on something observable.
func BundleStats(data []byte) (types int, nodes int, root uint64, source string, err error) {
	r := &reader{data: data}
	magic, err := r.take(uint64(len(Magic)))
	if err != nil || string(magic) != string(Magic) {
		return 0, 0, 0, "", errBadMagic
	}
	if _, err = r.uvarint(); err != nil {
		return 0, 0, 0, "", err
	}
	if source, err = r.str(); err != nil {
		return 0, 0, 0, "", err
	}
	sourceLen, err := r.uvarint()
	if err != nil {
		return 0, 0, 0, "", err
	}
	if _, err = r.take(sourceLen); err != nil {
		return 0, 0, 0, "", err
	}
	fileTypes, err := readTypeTable(r)
	if err != nil {
		return 0, 0, 0, "", err
	}
	nodeCount, err := r.uvarint()
	if err != nil {
		return 0, 0, 0, "", err
	}
	for i := uint64(0); i < nodeCount; i++ {
		length, err := r.uvarint()
		if err != nil {
			return 0, 0, 0, "", err
		}
		if _, err = r.take(length); err != nil {
			return 0, 0, 0, "", err
		}
	}
	if root, err = r.uvarint(); err != nil {
		return 0, 0, 0, "", err
	}
	return len(fileTypes), int(nodeCount), root, source, nil
}
