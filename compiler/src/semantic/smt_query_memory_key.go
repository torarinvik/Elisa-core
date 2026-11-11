package semantic

import "crypto/sha256"

// Large exact queries can account for hundreds of MiB in the resumable
// analysis state. Use the same SHA-256 collision resistance as the persistent
// proof cache, without alpha renaming or changing the query sent to the solver.
func smtQueryMemoryKey(query string) string {
	if len(query) > 4096 {
		digest := sha256.Sum256([]byte(query))
		return "\x00\x01" + string(digest[:])
	}
	// A raw input cannot occupy the digest namespace, even if malformed.
	if len(query) > 0 && query[0] == 0 {
		return "\x00\x00" + query
	}
	return query
}
