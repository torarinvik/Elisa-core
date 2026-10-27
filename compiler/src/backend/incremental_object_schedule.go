package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// These bounded, advisory records remember where generic bodies were actually
// emitted. They only order work; current exact bitcode still authorizes reuse.
const partitionScheduleLimit = 4 << 20

// Advisory only: this partition previously required whole-module lowering.
const partitionScheduleDeclined = "\x00partition-helper-declined-v1"

func partitionScheduleStem(name string) string {
	stem, _, _ := strings.Cut(name, "$generic_v1$")
	return stem
}

func (d *incrementalObjectDisk) schedulePath(unit, target [32]byte, parts int) string {
	key := sha256.Sum256([]byte("ir-schedule-v1:" + hex.EncodeToString(unit[:]) + ":" + hex.EncodeToString(target[:]) + ":" + strconv.Itoa(parts)))
	return filepath.Join(d.root, hex.EncodeToString(key[:])+".schedule")
}

func (d *incrementalObjectDisk) loadSchedule(unit, target [32]byte, parts int) [][]string {
	if d == nil || parts < 1 || parts > maxNativeObjectPartitions {
		return nil
	}
	f, err := os.Open(d.schedulePath(unit, target, parts))
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, partitionScheduleLimit+1))
	if err != nil || len(data) <= sha256.Size || len(data) > partitionScheduleLimit {
		return nil
	}
	digest := sha256.Sum256(data[sha256.Size:])
	if !bytes.Equal(data[:sha256.Size], digest[:]) {
		return nil
	}
	var members [][]string
	if json.Unmarshal(data[sha256.Size:], &members) != nil || len(members) != parts {
		return nil
	}
	for _, names := range members {
		for _, name := range names {
			if len(name) > 4096 {
				return nil
			}
		}
	}
	return members
}

func (d *incrementalObjectDisk) storeSchedule(unit, target [32]byte, members [][]string) {
	if d == nil || len(members) < 1 || len(members) > maxNativeObjectPartitions {
		return
	}
	for part, names := range members {
		sort.Strings(names)
		unique := names[:0]
		for _, name := range names {
			if len(name) > 4096 {
				return
			}
			if len(unique) == 0 || unique[len(unique)-1] != name {
				unique = append(unique, name)
			}
		}
		members[part] = unique
	}
	data, err := json.Marshal(members)
	if err != nil || len(data)+sha256.Size > partitionScheduleLimit || int64(len(data)+sha256.Size) > d.maxBytes {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if os.MkdirAll(d.root, 0700) != nil {
		return
	}
	f, err := os.CreateTemp(d.root, ".schedule-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	digest := sha256.Sum256(data)
	_, err = f.Write(digest[:])
	if err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(f.Name(), d.schedulePath(unit, target, len(members)))
		d.prune()
	}
}
