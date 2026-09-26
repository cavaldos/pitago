package pirpc

import (
	"bytes"
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data through a temp file in the same
// directory + rename.
//
// Why not os.WriteFile: that truncates in place, so a crash (or a power cut)
// between truncate and write leaves an empty or half-written file. For the
// files pitago shares with pi that is not a cosmetic problem: a truncated
// auth.json reads as "logged out everywhere" and a truncated settings.json
// as "all pi settings gone". rename(2) is atomic on POSIX, so a reader
// (including pi itself) sees either the old file or the new one, never a
// half-written one.
//
// The payload is fsynced before the rename and the directory is fsynced
// after it, so the swap survives a crash, not just a concurrent read.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	_, err := writeFileAtomicCAS(path, nil, data, perm)
	return err
}

// writeFileAtomicCAS is writeFileAtomic with a compare-and-swap guard: when
// expect is non-nil, path must still hold exactly those bytes at the moment
// of the rename, otherwise nothing is written and it reports (false, nil).
//
// It exists for the files pi writes itself. pi saves settings.json on its own
// (set_steering_mode, set_auto_compaction, …), so a plain read-modify-write
// could rename over a change pi made in between and drop it — a silent edit
// of the user's pi configuration. The caller then re-reads and retries on the
// newer file.
//
// The check narrows the window to the microseconds between the final read and
// rename(2); it is not a lock, and pi takes no lock either. Everything else
// (fsync payload, fsync dir, same-directory temp file) is identical to
// writeFileAtomic.
func writeFileAtomicCAS(path string, expect, data []byte, perm os.FileMode) (swapped bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	// Same directory as the target: rename is only atomic within a
	// filesystem, and a temp file in /tmp could land on another one.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	// No-op once the rename succeeded (the name is gone by then); it only
	// cleans up when a step below failed.
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	// CreateTemp is 0600; make the swapped-in file match what the plain
	// write would have produced (or whatever the file already had).
	if err := os.Chmod(name, perm); err != nil {
		return false, err
	}
	if expect != nil {
		cur, rerr := os.ReadFile(path)
		switch {
		case rerr != nil && !os.IsNotExist(rerr):
			return false, rerr
		case rerr != nil && len(expect) > 0:
			return false, nil // the file appeared under us: not ours to overwrite
		case !bytes.Equal(cur, expect):
			return false, nil // someone else wrote it; leave their version alone
		}
	}
	if err := os.Rename(name, path); err != nil {
		return false, err
	}
	syncDir(dir)
	return true, nil
}

// filePerm returns the permissions to write path with: the current mode when
// the file exists (a plain os.WriteFile only applies its mode on create), or
// def for a new file.
func filePerm(path string, def os.FileMode) os.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return def
}

// syncDir fsyncs a directory so a rename inside it is durable. Best effort:
// some filesystems reject fsync on a directory fd, and there is nothing to
// recover by failing the write over that.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
